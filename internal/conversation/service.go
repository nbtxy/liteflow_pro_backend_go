package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
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

	_, err = tx.Exec(ctx, `DELETE FROM messages WHERE conversation_id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}

	// scheduled_tasks and task_executions are cascade-deleted by FK
	_, err = tx.Exec(ctx, `DELETE FROM conversations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *Service) GetMessages(ctx context.Context, conversationID uuid.UUID) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, conversation_id, role, content, token_count, metadata, created_at
		 FROM messages WHERE conversation_id = $1 ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var msgs []domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content,
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

	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, role, content, token_count, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET content = $4, token_count = $5, metadata = $6`,
		msg.ID, msg.ConversationID, msg.Role, msg.Content, msg.TokenCount, msg.Metadata, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}

	s.pool.Exec(ctx, `UPDATE conversations SET updated_at = $1 WHERE id = $2`, time.Now(), msg.ConversationID)
	return nil
}

func (s *Service) DeleteMessage(ctx context.Context, messageID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, messageID)
	return err
}

func (s *Service) GetMessageByID(ctx context.Context, messageID uuid.UUID) (*domain.Message, error) {
	var m domain.Message
	err := s.pool.QueryRow(ctx,
		`SELECT id, conversation_id, role, content, token_count, metadata, created_at
		 FROM messages WHERE id = $1`, messageID).Scan(
		&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.TokenCount, &m.Metadata, &m.CreatedAt)
	if err == pgx.ErrNoRows {
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
