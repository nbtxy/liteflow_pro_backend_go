package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type UserSummary struct {
	domain.User
	ConversationCount int `json:"conversationCount"`
	MessageCount      int `json:"messageCount"`
}

func (s *Service) ListUsers(ctx context.Context, limit, offset int) ([]UserSummary, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.phone, u.name, u.is_admin, u.settings, u.created_at, u.updated_at,
		        (SELECT COUNT(*) FROM conversations c WHERE c.user_id = u.id) as conv_count,
		        (SELECT COUNT(*) FROM messages m JOIN conversations c ON m.conversation_id = c.id WHERE c.user_id = u.id) as msg_count
		 FROM users u ORDER BY u.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []UserSummary
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Phone, &u.Name, &u.IsAdmin, &u.Settings,
			&u.CreatedAt, &u.UpdatedAt, &u.ConversationCount, &u.MessageCount); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (s *Service) GetSystemStats(ctx context.Context) (map[string]any, error) {
	stats := map[string]any{}

	var userCount, convCount, msgCount, taskCount int64
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&userCount)
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations`).Scan(&convCount)
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE status = 'active'`).Scan(&taskCount)

	var dailyTokens int64
	s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0) FROM token_usage WHERE created_at > now() - interval '24 hours'`,
	).Scan(&dailyTokens)

	stats["userCount"] = userCount
	stats["conversationCount"] = convCount
	stats["messageCount"] = msgCount
	stats["activeTaskCount"] = taskCount
	stats["dailyTokens"] = dailyTokens
	stats["timestamp"] = time.Now()

	return stats, nil
}

func (s *Service) SetAdmin(ctx context.Context, userID uuid.UUID, isAdmin bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET is_admin = $1, updated_at = $2 WHERE id = $3`,
		isAdmin, time.Now(), userID)
	return err
}

func (s *Service) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tx.Exec(ctx, `DELETE FROM token_usage WHERE user_id = $1`, userID)
	tx.Exec(ctx, `DELETE FROM feedbacks WHERE user_id = $1`, userID)
	tx.Exec(ctx, `DELETE FROM user_memories WHERE user_id = $1`, userID)
	tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
	tx.Exec(ctx,
		`DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE user_id = $1)`, userID)
	tx.Exec(ctx, `DELETE FROM conversations WHERE user_id = $1`, userID)
	tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)

	return tx.Commit(ctx)
}
