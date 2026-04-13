package user

import (
	"context"
	"encoding/json"
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

func (s *Service) GetByID(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, phone, name, is_admin, settings, created_at, updated_at
		 FROM users WHERE id = $1`, userID).Scan(
		&u.ID, &u.Phone, &u.Name, &u.IsAdmin, &u.Settings, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) UpdateName(ctx context.Context, userID uuid.UUID, name string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET name = $1, updated_at = $2 WHERE id = $3`,
		name, time.Now(), userID)
	return err
}

func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, settings json.RawMessage) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET settings = $1, updated_at = $2 WHERE id = $3`,
		settings, time.Now(), userID)
	return err
}

func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (*domain.UserSettings, error) {
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(settings, '{}') FROM users WHERE id = $1`, userID).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}

	var settings domain.UserSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}
