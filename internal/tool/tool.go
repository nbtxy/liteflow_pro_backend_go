package tool

import (
	"context"

	"github.com/google/uuid"
)

// ToolResult is the result of executing a tool.
type ToolResult struct {
	Content  string         `json:"content"`
	IsError  bool           `json:"isError"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ToolContext provides execution context for tools.
type ToolContext struct {
	ConversationID uuid.UUID
	MessageID      uuid.UUID
	UserID         uuid.UUID
}

// Tool is the interface that all tools must implement.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error)
}
