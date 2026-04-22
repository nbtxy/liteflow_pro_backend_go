package task

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
)

type FeishuSender interface {
	SendMarkdownToUser(ctx context.Context, userID uuid.UUID, markdown string) error
}

type DefaultOutputSender struct {
	pool         *pgxpool.Pool
	feishuSender FeishuSender
}

func NewOutputSender(pool *pgxpool.Pool, feishuSender FeishuSender) *DefaultOutputSender {
	return &DefaultOutputSender{pool: pool, feishuSender: feishuSender}
}

func (s *DefaultOutputSender) SendToConversation(ctx context.Context, userID, convID uuid.UUID, taskName, content string) error {
	text := fmt.Sprintf("**[定时任务: %s]**\n\n%s", taskName, content)
	msg := &domain.Message{
		ID:             uuid.New(),
		ConversationID: convID,
		Role:           "assistant",
		CreatedAt:      time.Now(),
	}
	if parts, err := json.Marshal([]map[string]any{{"type": "text", "text": text}}); err == nil {
		msg.ContentParts = parts
	}
	tc := int32(len(content))
	msg.TokenCount = &tc

	meta, _ := json.Marshal(map[string]any{
		"source":   "scheduled_task",
		"taskName": taskName,
	})
	msg.Metadata = meta

	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, role, content_parts, token_count, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		msg.ID, msg.ConversationID, msg.Role, msg.ContentParts, msg.TokenCount, msg.Metadata, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("save task result message: %w", err)
	}

	s.pool.Exec(ctx, `UPDATE conversations SET updated_at = $1 WHERE id = $2`, time.Now(), convID)
	slog.Info("sent task result to conversation", "convId", convID)
	return nil
}

func (s *DefaultOutputSender) SendToFeishu(ctx context.Context, userID uuid.UUID, taskName, content string) error {
	if s.feishuSender == nil {
		slog.Warn("feishu sender not configured, skipping feishu output")
		return nil
	}
	markdown := fmt.Sprintf("**[定时任务: %s]**\n\n%s", taskName, content)
	return s.feishuSender.SendMarkdownToUser(ctx, userID, markdown)
}
