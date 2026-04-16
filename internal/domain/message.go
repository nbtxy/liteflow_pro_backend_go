package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Message struct {
	ID              uuid.UUID       `json:"id"`
	ConversationID  uuid.UUID       `json:"conversationId"`
	Role            string          `json:"role"` // system, user, assistant, tool
	SenderType      string          `json:"senderType,omitempty"`
	AgentID         *string         `json:"agentId,omitempty"`
	ParentMessageID *uuid.UUID      `json:"parentMessageId,omitempty"`
	IsInternal      bool            `json:"isInternal,omitempty"`
	Content         string          `json:"content"`
	TokenCount      *int32          `json:"tokenCount,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"` // JSONB: tool_calls, etc.
	CreatedAt       time.Time       `json:"createdAt"`
}

// MessageMetadata represents the metadata stored in JSONB.
type MessageMetadata struct {
	ToolCalls   []ToolCallInfo `json:"tool_calls,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	ToolName    string         `json:"tool_name,omitempty"`
	Attachments []Attachment   `json:"attachments,omitempty"`
}

type ToolCallInfo struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Attachment struct {
	Type string `json:"type"` // image, file
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}
