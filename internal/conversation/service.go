package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/platform/storage"
)

type Service struct {
	pool       *pgxpool.Pool
	storageSvc storage.Service
}

func NewService(pool *pgxpool.Pool, storageSvc storage.Service) *Service {
	return &Service{
		pool:       pool,
		storageSvc: storageSvc,
	}
}

func (s *Service) Create(ctx context.Context, userID uuid.UUID) (*domain.Conversation, error) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:        uuid.New(),
		UserID:    &userID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	archived := false
	conv.Archived = &archived

	_, err := s.pool.Exec(ctx,
		`INSERT INTO conversations (id, user_id, created_at, updated_at, archived)
		 VALUES ($1, $2, $3, $4, $5)`,
		conv.ID, conv.UserID, conv.CreatedAt, conv.UpdatedAt, conv.Archived)
	if err != nil {
		return nil, fmt.Errorf("create conversation: %w", err)
	}
	return conv, nil
}

func (s *Service) GetByIDAndUserID(ctx context.Context, id, userID uuid.UUID) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, title, created_at, updated_at, memory_extracted, archived,
		        channel_id, external_chat_id, channel_type
		 FROM conversations WHERE id = $1 AND user_id = $2`, id, userID).Scan(
		&conv.ID, &conv.UserID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt,
		&conv.MemoryExtracted, &conv.Archived, &conv.ChannelID, &conv.ExternalChatID, &conv.ChannelType,
	)
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func (s *Service) ListPaged(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Conversation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, title, created_at, updated_at, memory_extracted, archived
		 FROM conversations
		 WHERE user_id = $1 AND (archived IS NULL OR archived = FALSE)
		 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var convs []domain.Conversation
	for rows.Next() {
		var c domain.Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt,
			&c.MemoryExtracted, &c.Archived); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, nil
}

func (s *Service) CountActive(ctx context.Context, userID uuid.UUID) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM conversations WHERE user_id = $1 AND (archived IS NULL OR archived = FALSE)`,
		userID).Scan(&count)
	return count, err
}

func (s *Service) ListArchived(ctx context.Context, userID uuid.UUID) ([]domain.Conversation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, title, created_at, updated_at, memory_extracted, archived
		 FROM conversations
		 WHERE user_id = $1 AND archived = TRUE
		 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list archived: %w", err)
	}
	defer rows.Close()

	var convs []domain.Conversation
	for rows.Next() {
		var c domain.Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt,
			&c.MemoryExtracted, &c.Archived); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, nil
}

func (s *Service) Unarchive(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE conversations SET archived = false, updated_at = $1 WHERE id = $2 AND user_id = $3`,
		time.Now(), id, userID)
	return err
}

func (s *Service) BatchArchive(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if err := s.Archive(ctx, id, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) BatchDelete(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if err := s.Delete(ctx, id, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateTitle(ctx context.Context, id uuid.UUID, title string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE conversations SET title = $1, updated_at = $2 WHERE id = $3`,
		title, time.Now(), id)
	return err
}

func (s *Service) Archive(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE conversations SET archived = true, updated_at = $1 WHERE id = $2 AND user_id = $3`,
		time.Now(), id, userID)
	return err
}

func (s *Service) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var filePaths []string
	if s.storageSvc != nil {
		filePaths, err = s.listConversationFilePathsTx(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("list conversation files: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `DELETE FROM messages WHERE conversation_id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}

	// scheduled_tasks and task_executions are cascade-deleted by FK
	_, err = tx.Exec(ctx, `DELETE FROM conversations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.cleanupConversationFiles(ctx, id, filePaths)
	return nil
}

func (s *Service) listConversationFilePathsTx(ctx context.Context, tx pgx.Tx, conversationID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT file_path
		 FROM artifacts
		 WHERE conversation_id = $1 AND file_path <> ''`,
		conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make([]string, 0, 8)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func (s *Service) cleanupConversationFiles(ctx context.Context, conversationID uuid.UUID, filePaths []string) {
	if s.storageSvc == nil {
		return
	}

	if cleaner, ok := s.storageSvc.(interface {
		DeleteConversation(context.Context, string) error
	}); ok {
		if err := cleaner.DeleteConversation(ctx, conversationID.String()); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("failed to cleanup conversation directory",
				"conversationId", conversationID.String(),
				"err", err)
		}
		return
	}

	for _, path := range filePaths {
		if path == "" {
			continue
		}
		if err := s.storageSvc.DeleteFile(ctx, conversationID.String(), path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("failed to delete conversation file",
				"conversationId", conversationID.String(),
				"path", path,
				"err", err)
		}
	}
}

func (s *Service) GetMessages(ctx context.Context, conversationID uuid.UUID) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, conversation_id, role, sender_type, agent_id, parent_message_id, is_internal, content, token_count, metadata, created_at
		 FROM messages
		 WHERE conversation_id = $1
		   AND (is_internal IS NULL OR is_internal = FALSE)
		 ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var msgs []domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.SenderType, &m.AgentID, &m.ParentMessageID, &m.IsInternal, &m.Content,
			&m.TokenCount, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (s *Service) SaveMessage(ctx context.Context, msg *domain.Message) error {
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}

	if msg.SenderType == "" {
		msg.SenderType = "user"
		if msg.Role == "assistant" {
			msg.SenderType = "agent"
		}
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, role, sender_type, agent_id, parent_message_id, is_internal, content, token_count, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (id) DO UPDATE SET sender_type = $4, agent_id = $5, parent_message_id = $6, is_internal = $7, content = $8, token_count = $9, metadata = $10`,
		msg.ID, msg.ConversationID, msg.Role, msg.SenderType, msg.AgentID, msg.ParentMessageID, msg.IsInternal, msg.Content, msg.TokenCount, msg.Metadata, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}

	s.pool.Exec(ctx, `UPDATE conversations SET updated_at = $1 WHERE id = $2`, time.Now(), msg.ConversationID)
	return nil
}

func (s *Service) DeleteMessage(ctx context.Context, messageID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var conversationID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT conversation_id FROM messages WHERE id = $1`, messageID).Scan(&conversationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("get conversation id by message: %w", err)
	}

	filePaths, err := s.listDeletableFilePathsByMessageTx(ctx, tx, messageID)
	if err != nil {
		return fmt.Errorf("list deletable file paths by message: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE artifacts
		 SET file_deleted = true
		 WHERE message_id = $1`,
		messageID); err != nil {
		return fmt.Errorf("mark artifacts deleted by message: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM messages WHERE id = $1`, messageID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if s.storageSvc != nil {
		for _, path := range filePaths {
			if path == "" {
				continue
			}
			if err := s.storageSvc.DeleteFile(ctx, conversationID.String(), path); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("failed to cleanup file by deleted message",
					"conversationId", conversationID.String(),
					"messageId", messageID.String(),
					"path", path,
					"err", err)
			}
		}
	}

	return nil
}

func (s *Service) listDeletableFilePathsByMessageTx(ctx context.Context, tx pgx.Tx, messageID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT a.file_path
		 FROM artifacts a
		 WHERE a.message_id = $1
		   AND a.file_path <> ''
		   AND (a.file_deleted IS NULL OR a.file_deleted = FALSE)
		   AND NOT EXISTS (
		     SELECT 1
		     FROM artifacts b
		     WHERE b.conversation_id = a.conversation_id
		       AND b.file_path = a.file_path
		       AND (b.file_deleted IS NULL OR b.file_deleted = FALSE)
		       AND (b.message_id IS DISTINCT FROM $1)
		   )`,
		messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make([]string, 0, 8)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func (s *Service) GetMessageByID(ctx context.Context, messageID uuid.UUID) (*domain.Message, error) {
	var m domain.Message
	err := s.pool.QueryRow(ctx,
		`SELECT id, conversation_id, role, sender_type, agent_id, parent_message_id, is_internal, content, token_count, metadata, created_at
		 FROM messages WHERE id = $1`, messageID).Scan(
		&m.ID, &m.ConversationID, &m.Role, &m.SenderType, &m.AgentID, &m.ParentMessageID, &m.IsInternal, &m.Content, &m.TokenCount, &m.Metadata, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Service) GetLastAgentMessage(ctx context.Context, conversationID uuid.UUID) (*domain.Message, error) {
	var m domain.Message
	err := s.pool.QueryRow(ctx, `
		SELECT id, conversation_id, role, sender_type, agent_id, parent_message_id, is_internal, content, token_count, metadata, created_at
		FROM messages
		WHERE conversation_id = $1
		  AND role = 'assistant'
		  AND agent_id IS NOT NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, conversationID).Scan(
		&m.ID, &m.ConversationID, &m.Role, &m.SenderType, &m.AgentID, &m.ParentMessageID, &m.IsInternal, &m.Content, &m.TokenCount, &m.Metadata, &m.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Service) ClearMessages(ctx context.Context, conversationID, userID uuid.UUID) error {
	conv, err := s.GetByIDAndUserID(ctx, conversationID, userID)
	if err != nil || conv == nil {
		return fmt.Errorf("conversation not found")
	}

	_, err = s.pool.Exec(ctx, `DELETE FROM messages WHERE conversation_id = $1`, conversationID)
	if err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}

	slog.Info("cleared messages", "conversationId", conversationID)
	return nil
}

type MCPState struct {
	Mode            string   `json:"mode"`
	ActivatedTools  []string `json:"activated_tools"`
	SourceMessageID *string  `json:"source_message_id,omitempty"`
}

func (s *Service) GetMCPState(ctx context.Context, conversationID, userID uuid.UUID) (*MCPState, error) {
	var raw []byte

	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(mcp_state, '{"mode":"inactive","activated_tools":[]}'::jsonb)
		 FROM conversations
		 WHERE id = $1 AND user_id = $2`,
		conversationID, userID).Scan(&raw)
	if err != nil {
		return nil, err
	}

	state, err := decodeMCPState(raw)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Service) GetMCPStateWithPrev(ctx context.Context, conversationID, userID uuid.UUID) (*MCPState, *MCPState, error) {
	var currentRaw []byte
	var prevRaw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(mcp_state, '{"mode":"inactive","activated_tools":[]}'::jsonb),
		        mcp_prev_state
		 FROM conversations
		 WHERE id = $1 AND user_id = $2`,
		conversationID, userID).Scan(&currentRaw, &prevRaw)
	if err != nil {
		return nil, nil, err
	}

	currentState, err := decodeMCPState(currentRaw)
	if err != nil {
		return nil, nil, err
	}
	if len(prevRaw) == 0 {
		return currentState, nil, nil
	}

	prevState, err := decodeMCPState(prevRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("decode mcp_prev_state: %w", err)
	}
	return currentState, prevState, nil
}

func (s *Service) SetMCPState(ctx context.Context, conversationID, userID uuid.UUID, state *MCPState) error {
	state = normalizeMCPState(state)
	if state.SourceMessageID != nil && *state.SourceMessageID != "" {
		if _, err := uuid.Parse(*state.SourceMessageID); err != nil {
			return fmt.Errorf("invalid source_message_id: %w", err)
		}
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode mcp_state: %w", err)
	}

	cmd, err := s.pool.Exec(ctx,
		`UPDATE conversations
		 SET mcp_prev_state = mcp_state,
		     mcp_state = $1
		 WHERE id = $2 AND user_id = $3`,
		stateJSON, conversationID, userID)
	if err != nil {
		return fmt.Errorf("update mcp state: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func decodeMCPState(raw []byte) (*MCPState, error) {
	state := &MCPState{
		Mode:           "inactive",
		ActivatedTools: []string{},
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, state); err != nil {
			return nil, fmt.Errorf("decode mcp_state: %w", err)
		}
	}
	return normalizeMCPState(state), nil
}

func normalizeMCPState(state *MCPState) *MCPState {
	if state == nil {
		state = &MCPState{}
	}
	if state.Mode == "" {
		state.Mode = "inactive"
	}
	if state.Mode != "active" {
		state.Mode = "inactive"
		state.ActivatedTools = []string{}
		state.SourceMessageID = nil
	}
	if state.ActivatedTools == nil {
		state.ActivatedTools = []string{}
	}
	return state
}
