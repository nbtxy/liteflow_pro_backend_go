package domain

import (
	"time"

	"github.com/google/uuid"
)

type SmsCode struct {
	ID        uuid.UUID `json:"id"`
	Phone     string    `json:"phone"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"createdAt"`
}
