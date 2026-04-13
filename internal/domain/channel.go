package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type UserChannel struct {
	ID                   uuid.UUID       `json:"id"`
	UserID               uuid.UUID       `json:"userId"`
	Type                 string          `json:"type"`        // im, mcp
	Name                 *string         `json:"name"`        // feishu, github, etc.
	DisplayName          *string         `json:"displayName"`
	Config               json.RawMessage `json:"config"`      // JSONB
	Status               string          `json:"status"`      // pending, active, stopped, error
	ErrorMessage         *string         `json:"errorMessage,omitempty"`
	AuthorizedAccountID  *string         `json:"authorizedAccountId,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`

	// Transient
	ToolCount *int `json:"toolCount,omitempty"`
}

type ChannelUserMapping struct {
	ID             uuid.UUID  `json:"id"`
	ChannelID      uuid.UUID  `json:"channelId"`
	ExternalUserID string     `json:"externalUserId"`
	ExternalName   *string    `json:"externalName,omitempty"`
	LiteflowUserID *uuid.UUID `json:"liteflowUserId,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}
