package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Feedback struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"userId"`
	ConversationID uuid.UUID       `json:"conversationId"`
	MessageID      uuid.UUID       `json:"messageId"`
	Rating         string          `json:"rating"`
	Reasons        json.RawMessage `json:"reasons,omitempty"`
	Comment        *string         `json:"comment,omitempty"`
	Context        json.RawMessage `json:"context,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}
