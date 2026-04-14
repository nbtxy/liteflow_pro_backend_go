package usage

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

func (s *Service) GetUserUsage(ctx context.Context, userID uuid.UUID, from, to time.Time) (map[string]any, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT provider, input_tokens, output_tokens, created_at
		 FROM token_usage
		 WHERE user_id = $1 AND created_at >= $2 AND created_at <= $3
		 ORDER BY created_at ASC`,
		userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get user usage: %w", err)
	}
	defer rows.Close()

	type providerStats struct {
		input  int64
		output int64
		calls  int64
		cost   float64
	}
	type dailyStats struct {
		input  int64
		output int64
	}

	totalInput := int64(0)
	totalOutput := int64(0)
	totalCalls := int64(0)
	totalCost := 0.0
	byProvider := map[string]*providerStats{}
	byDay := map[string]*dailyStats{}

	for rows.Next() {
		var provider string
		var inputTokens, outputTokens int64
		var createdAt time.Time
		if err := rows.Scan(&provider, &inputTokens, &outputTokens, &createdAt); err != nil {
			return nil, err
		}

		totalInput += inputTokens
		totalOutput += outputTokens
		totalCalls++

		cost := EstimateCost(provider, int(inputTokens), int(outputTokens))
		totalCost += cost

		ps := byProvider[provider]
		if ps == nil {
			ps = &providerStats{}
			byProvider[provider] = ps
		}
		ps.input += inputTokens
		ps.output += outputTokens
		ps.calls++
		ps.cost += cost

		dayKey := createdAt.Local().Format("2006-01-02")
		ds := byDay[dayKey]
		if ds == nil {
			ds = &dailyStats{}
			byDay[dayKey] = ds
		}
		ds.input += inputTokens
		ds.output += outputTokens
	}

	byProviderResp := map[string]any{}
	for provider, stats := range byProvider {
		byProviderResp[provider] = map[string]any{
			"inputTokens":   stats.input,
			"outputTokens":  stats.output,
			"apiCalls":      stats.calls,
			"estimatedCost": fmt.Sprintf("$%.4f", stats.cost),
		}
	}

	dayKeys := make([]string, 0, len(byDay))
	for k := range byDay {
		dayKeys = append(dayKeys, k)
	}
	sort.Strings(dayKeys)

	daily := make([]map[string]any, 0, len(dayKeys))
	for _, day := range dayKeys {
		stats := byDay[day]
		daily = append(daily, map[string]any{
			"date":         day,
			"inputTokens":  stats.input,
			"outputTokens": stats.output,
		})
	}

	return map[string]any{
		"period": map[string]any{
			"from": from.Format(time.RFC3339),
			"to":   to.Format(time.RFC3339),
		},
		"total": map[string]any{
			"inputTokens":   totalInput,
			"outputTokens":  totalOutput,
			"totalTokens":   totalInput + totalOutput,
			"apiCalls":      totalCalls,
			"estimatedCost": fmt.Sprintf("$%.4f", totalCost),
		},
		"byProvider": byProviderResp,
		"daily":      daily,
	}, nil
}
