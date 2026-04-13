package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type McpTool struct {
	ID          uuid.UUID       `json:"id"`
	ChannelID   uuid.UUID       `json:"channelId"`
	UserID      uuid.UUID       `json:"userId"`
	ToolName    string          `json:"toolName"`
	DisplayName string          `json:"displayName"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Category    *string         `json:"category,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt"`

	// Transient (populated via join)
	ChannelName string `json:"channelName,omitempty"`
}
