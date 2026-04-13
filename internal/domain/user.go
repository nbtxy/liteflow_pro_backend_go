package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID       `json:"id"`
	Phone     string          `json:"phone"`
	Name      *string         `json:"name"`
	IsAdmin   bool            `json:"isAdmin"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type UserSettings struct {
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	SystemPrompt   string `json:"systemPrompt,omitempty"`
	MaxTokens      int    `json:"maxTokens,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
}
