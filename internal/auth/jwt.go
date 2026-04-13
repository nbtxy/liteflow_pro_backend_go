package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/config"
)

type JwtService struct {
	secret     []byte
	expiration time.Duration
}

func NewJwtService(cfg config.JWTConfig) *JwtService {
	return &JwtService{
		secret:     []byte(cfg.Secret),
		expiration: cfg.AccessTokenDuration(),
	}
}

func (s *JwtService) GenerateToken(userID uuid.UUID) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.expiration)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *JwtService) ParseUserID(tokenStr string) (uuid.UUID, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}

	sub, err := token.Claims.GetSubject()
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}

	return uuid.Parse(sub)
}

func (s *JwtService) IsValid(tokenStr string) bool {
	_, err := s.ParseUserID(tokenStr)
	return err == nil
}
