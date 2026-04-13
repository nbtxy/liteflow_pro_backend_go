package domain

import (
	"time"

	"github.com/google/uuid"
)

type TokenUsage struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"userId"`
	ConversationID *uuid.UUID `json:"conversationId,omitempty"`
	MessageID      *uuid.UUID `json:"messageId,omitempty"`
	Provider       string     `json:"provider"`
	Model          string     `json:"model"`
	InputTokens    int32      `json:"inputTokens"`
	OutputTokens   int32      `json:"outputTokens"`
	Purpose        string     `json:"purpose"` // chat, chat_with_tools, title_gen, memory_extract
	Channel        string     `json:"channel"` // web, feishu
	DurationMs     *int32     `json:"durationMs,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}
