package usage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/llm"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) RecordAsync(ctx context.Context, userID, conversationID, messageID uuid.UUID,
	provider string, usage *llm.LlmUsage, purpose, channel string, durationMs int32) {
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.Record(bgCtx, userID, conversationID, messageID,
			provider, usage, purpose, channel, durationMs); err != nil {
			slog.Error("failed to record token usage", "err", err, "userId", userID)
		}
	}()
}

func (s *Service) Record(ctx context.Context, userID, conversationID, messageID uuid.UUID,
	provider string, usage *llm.LlmUsage, purpose, channel string, durationMs int32) error {

	model := provider
	_, err := s.pool.Exec(ctx,
		`INSERT INTO token_usage (user_id, conversation_id, message_id, provider, model,
		 input_tokens, output_tokens, purpose, channel, duration_ms, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		userID, conversationID, messageID, provider, model,
		usage.InputTokens, usage.OutputTokens, purpose, channel, durationMs, time.Now())
	if err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}

func (s *Service) DeleteByMessageID(ctx context.Context, messageID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM token_usage WHERE message_id = $1`, messageID)
	return err
}

func (s *Service) GetUserDailySummary(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	var totalInput, totalOutput int64
	var totalRequests int64

	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COUNT(*)
		 FROM token_usage WHERE user_id = $1 AND created_at > now() - interval '24 hours'`,
		userID).Scan(&totalInput, &totalOutput, &totalRequests)
	if err != nil {
		return nil, fmt.Errorf("get daily summary: %w", err)
	}

	return map[string]any{
		"totalInputTokens":  totalInput,
		"totalOutputTokens": totalOutput,
		"totalTokens":       totalInput + totalOutput,
		"totalRequests":     totalRequests,
	}, nil
}

func (s *Service) GetUserHistory(ctx context.Context, userID uuid.UUID, limit int) ([]domain.TokenUsage, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, conversation_id, message_id, provider, model,
		        input_tokens, output_tokens, purpose, channel, duration_ms, created_at
		 FROM token_usage WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get usage history: %w", err)
	}
	defer rows.Close()

	var usages []domain.TokenUsage
	for rows.Next() {
		var u domain.TokenUsage
		if err := rows.Scan(&u.ID, &u.UserID, &u.ConversationID, &u.MessageID,
			&u.Provider, &u.Model, &u.InputTokens, &u.OutputTokens,
			&u.Purpose, &u.Channel, &u.DurationMs, &u.CreatedAt); err != nil {
			return nil, err
		}
		usages = append(usages, u)
	}
	return usages, nil
}
