package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/platform/sms"
)

type AuthService struct {
	pool       *pgxpool.Pool
	jwtSvc     *JwtService
	smsSvc     sms.Service
	cfg        config.JWTConfig
	adminPhone string
}

func NewAuthService(pool *pgxpool.Pool, jwtSvc *JwtService, smsSvc sms.Service, cfg config.JWTConfig, adminPhone string) *AuthService {
	return &AuthService{
		pool:       pool,
		jwtSvc:     jwtSvc,
		smsSvc:     smsSvc,
		cfg:        cfg,
		adminPhone: adminPhone,
	}
}

type LoginResult struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	UserID       uuid.UUID `json:"userId"`
	IsNewUser    bool      `json:"isNewUser"`
}

func (s *AuthService) SendCode(ctx context.Context, phone string) error {
	// Check rate limit: 60-second interval
	var lastSentAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT created_at FROM sms_codes WHERE phone = $1 AND used = false ORDER BY created_at DESC LIMIT 1`,
		phone).Scan(&lastSentAt)
	if err == nil && lastSentAt != nil && time.Since(*lastSentAt) < 60*time.Second {
		return ErrTooFrequent
	}

	// Check daily limit: max 10 per day
	var dailyCount int
	s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sms_codes WHERE phone = $1 AND created_at > now() - interval '24 hours'`,
		phone).Scan(&dailyCount)
	if dailyCount >= 10 {
		return ErrDailyLimit
	}

	// Generate 6-digit code
	code := generateCode()

	// Save code to DB
	_, err = s.pool.Exec(ctx,
		`INSERT INTO sms_codes (phone, code, expires_at) VALUES ($1, $2, $3)`,
		phone, code, time.Now().Add(5*time.Minute))
	if err != nil {
		return fmt.Errorf("save sms code: %w", err)
	}

	// Send SMS
	if err := s.smsSvc.Send(ctx, phone, code); err != nil {
		slog.Error("failed to send sms", "phone", phone, "err", err)
		return fmt.Errorf("send sms: %w", err)
	}

	return nil
}

func (s *AuthService) Login(ctx context.Context, phone, code string) (*LoginResult, error) {
	// Verify code
	var codeID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM sms_codes WHERE phone = $1 AND code = $2 AND used = false AND expires_at > now() ORDER BY created_at DESC LIMIT 1`,
		phone, code).Scan(&codeID)
	if err != nil {
		return nil, ErrInvalidCode
	}

	// Mark code as used
	s.pool.Exec(ctx, `UPDATE sms_codes SET used = true WHERE id = $1`, codeID)

	// Find or create user
	var userID uuid.UUID
	var isNewUser bool

	err = s.pool.QueryRow(ctx, `SELECT id FROM users WHERE phone = $1`, phone).Scan(&userID)
	if err == pgx.ErrNoRows {
		isAdmin := phone == s.adminPhone
		err = s.pool.QueryRow(ctx,
			`INSERT INTO users (phone, is_admin) VALUES ($1, $2) RETURNING id`,
			phone, isAdmin).Scan(&userID)
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
		isNewUser = true
	} else if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}

	// Generate tokens
	accessToken, err := s.jwtSvc.GenerateToken(userID)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken := uuid.NewString()
	_, err = s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token, expires_at) VALUES ($1, $2, $3)`,
		userID, refreshToken, time.Now().Add(s.cfg.RefreshTokenDuration()))
	if err != nil {
		return nil, fmt.Errorf("save refresh token: %w", err)
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		UserID:       userID,
		IsNewUser:    isNewUser,
	}, nil
}

func (s *AuthService) Refresh(ctx context.Context, refreshTokenStr string) (*LoginResult, error) {
	var tokenID, userID uuid.UUID
	var expiresAt time.Time
	var revoked bool

	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, expires_at, revoked FROM refresh_tokens WHERE token = $1`,
		refreshTokenStr).Scan(&tokenID, &userID, &expiresAt, &revoked)
	if err != nil || revoked || time.Now().After(expiresAt) {
		return nil, ErrInvalidToken
	}

	// Revoke old token
	s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked = true WHERE id = $1`, tokenID)

	// Generate new tokens
	accessToken, err := s.jwtSvc.GenerateToken(userID)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	newRefreshToken := uuid.NewString()
	_, err = s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token, expires_at) VALUES ($1, $2, $3)`,
		userID, newRefreshToken, time.Now().Add(s.cfg.RefreshTokenDuration()))
	if err != nil {
		return nil, fmt.Errorf("save refresh token: %w", err)
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		UserID:       userID,
	}, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE token = $1`, refreshTokenStr)
	return err
}

func (s *AuthService) RevokeAllTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE user_id = $1 AND revoked = false`, userID)
	return err
}

// GetLatestCode returns the latest unused SMS code (for debug endpoint)
func (s *AuthService) GetLatestCode(ctx context.Context, phone string) (string, error) {
	var code string
	err := s.pool.QueryRow(ctx,
		`SELECT code FROM sms_codes WHERE phone = $1 AND used = false AND expires_at > now() ORDER BY created_at DESC LIMIT 1`,
		phone).Scan(&code)
	if err != nil {
		return "", err
	}
	return code, nil
}

func generateCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}
