package domain

import (
	"time"

	"github.com/google/uuid"
)

type Conversation struct {
	ID              uuid.UUID  `json:"id"`
	UserID          *uuid.UUID `json:"userId"`
	Title           *string    `json:"title"`
	ActiveStreamID  *string    `json:"activeStreamId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	MemoryExtracted *bool      `json:"memoryExtracted,omitempty"`
	Archived        *bool      `json:"archived,omitempty"`
	ChannelID       *uuid.UUID `json:"channelId,omitempty"`
	ExternalChatID  *string    `json:"externalChatId,omitempty"`
	ChannelType     *string    `json:"channelType,omitempty"`
}
