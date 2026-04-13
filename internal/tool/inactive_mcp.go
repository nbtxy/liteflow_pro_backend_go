package tool

import (
	"context"
)

type InactiveMCPTool struct{}

func NewInactiveMCP() *InactiveMCPTool {
	return &InactiveMCPTool{}
}

func (t *InactiveMCPTool) Name() string { return "inactive_mcp_tool" }
func (t *InactiveMCPTool) Description() string {
	return "Deactivate all currently active MCP tools and return to normal tool mode."
}

func (t *InactiveMCPTool) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *InactiveMCPTool) Execute(_ context.Context, _ map[string]any, _ *ToolContext) (*ToolResult, error) {
	return &ToolResult{
		Content: "MCP tools deactivated.",
		Metadata: map[string]any{
			"mcp_mode": "inactive",
		},
	}, nil
}
