package chat

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/agent"
)

type streamSession struct {
	id             string
	userID         uuid.UUID
	conversationID string
	createdAt      time.Time
	updatedAt      time.Time
	done           bool
	cancel         func()
	replay         []agent.Event
	subs           map[int]chan agent.Event
	nextSubID      int
}

type StreamManager struct {
	mu        sync.Mutex
	sessions  map[string]*streamSession
	maxReplay int
	ttl       time.Duration
}

func NewStreamManager(maxReplay int, ttl time.Duration) *StreamManager {
	if maxReplay <= 0 {
		maxReplay = 512
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &StreamManager{
		sessions:  make(map[string]*streamSession),
		maxReplay: maxReplay,
		ttl:       ttl,
	}
}

func (m *StreamManager) Start(streamID string, userID uuid.UUID, conversationID string, source <-chan agent.Event, cancel func()) (<-chan agent.Event, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleanupLocked()

	if streamID == "" {
		streamID = uuid.NewString()
	}

	if existing, ok := m.sessions[streamID]; ok {
		if existing.userID != userID {
			return nil, "", fmt.Errorf("stream belongs to another user")
		}
		ch := m.subscribeLocked(existing)
		return ch, streamID, nil
	}

	session := &streamSession{
		id:             streamID,
		userID:         userID,
		conversationID: conversationID,
		createdAt:      time.Now(),
		updatedAt:      time.Now(),
		cancel:         cancel,
		replay:         make([]agent.Event, 0, m.maxReplay),
		subs:           make(map[int]chan agent.Event),
	}
	m.sessions[streamID] = session
	ch := m.subscribeLocked(session)
	go m.fanout(streamID, source)
	return ch, streamID, nil
}

func (m *StreamManager) Subscribe(streamID string, userID uuid.UUID) (<-chan agent.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()

	session, ok := m.sessions[streamID]
	if !ok {
		return nil, fmt.Errorf("stream not found")
	}
	if session.userID != userID {
		return nil, fmt.Errorf("stream belongs to another user")
	}
	return m.subscribeLocked(session), nil
}

func (m *StreamManager) Stop(streamID string, userID uuid.UUID) bool {
	m.mu.Lock()
	session, ok := m.sessions[streamID]
	if !ok || session.userID != userID {
		m.mu.Unlock()
		return false
	}
	cancel := session.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

func (m *StreamManager) ActiveByConversation(userID uuid.UUID) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()

	out := make(map[string]string)
	updatedAt := make(map[string]time.Time)
	for _, session := range m.sessions {
		if session.userID != userID || session.done || session.conversationID == "" {
			continue
		}
		prev, ok := updatedAt[session.conversationID]
		if !ok || session.updatedAt.After(prev) {
			updatedAt[session.conversationID] = session.updatedAt
			out[session.conversationID] = session.id
		}
	}
	return out
}

func (m *StreamManager) subscribeLocked(session *streamSession) <-chan agent.Event {
	replay := append([]agent.Event(nil), session.replay...)
	done := session.done
	subID := session.nextSubID
	session.nextSubID++
	ch := make(chan agent.Event, m.maxReplay+64)
	if !done {
		session.subs[subID] = ch
	}

	go func() {
		for _, ev := range replay {
			ch <- ev
		}
		if done {
			close(ch)
		}
	}()
	return ch
}

func (m *StreamManager) fanout(streamID string, source <-chan agent.Event) {
	for ev := range source {
		m.mu.Lock()
		session, ok := m.sessions[streamID]
		if !ok {
			m.mu.Unlock()
			return
		}
		session.updatedAt = time.Now()
		if session.conversationID == "" {
			if conversationID, ok := ev.Data["conversationId"].(string); ok && conversationID != "" {
				session.conversationID = conversationID
			}
		}
		session.replay = append(session.replay, ev)
		if len(session.replay) > m.maxReplay {
			session.replay = session.replay[len(session.replay)-m.maxReplay:]
		}

		stale := make([]int, 0, 4)
		for subID, ch := range session.subs {
			select {
			case ch <- ev:
			default:
				stale = append(stale, subID)
			}
		}
		for _, subID := range stale {
			if ch, ok := session.subs[subID]; ok {
				close(ch)
				delete(session.subs, subID)
			}
		}
		m.mu.Unlock()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[streamID]
	if !ok {
		return
	}
	session.done = true
	session.updatedAt = time.Now()
	for subID, ch := range session.subs {
		close(ch)
		delete(session.subs, subID)
	}
	m.cleanupLocked()
}

func (m *StreamManager) cleanupLocked() {
	now := time.Now()
	for id, session := range m.sessions {
		if !session.done {
			continue
		}
		if now.Sub(session.updatedAt) > m.ttl {
			delete(m.sessions, id)
		}
	}
}
