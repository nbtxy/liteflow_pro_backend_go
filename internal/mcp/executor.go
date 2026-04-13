package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/tool"
)

type ToolExecutor struct {
	mcpTool   domain.McpTool
	client    *Client
	serverCfg ServerConfig
}

func NewToolExecutor(mcpTool domain.McpTool, client *Client, serverCfg ServerConfig) *ToolExecutor {
	return &ToolExecutor{mcpTool: mcpTool, client: client, serverCfg: serverCfg}
}

func (e *ToolExecutor) Name() string        { return e.mcpTool.ToolName }
func (e *ToolExecutor) Description() string  { return e.mcpTool.Description }

func (e *ToolExecutor) InputSchema() map[string]any {
	var schema map[string]any
	if e.mcpTool.InputSchema != nil {
		if err := json.Unmarshal(e.mcpTool.InputSchema, &schema); err == nil {
			return schema
		}
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (e *ToolExecutor) Execute(ctx context.Context, input map[string]any, tc *tool.ToolContext) (*tool.ToolResult, error) {
	slog.Info("executing MCP tool", "tool", e.mcpTool.ToolName)

	if e.client == nil {
		return &tool.ToolResult{Content: "MCP client not available", IsError: true}, nil
	}

	result, err := e.client.CallTool(ctx, e.serverCfg, e.mcpTool.ToolName, input)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("MCP call failed: %v", err), IsError: true}, nil
	}

	return &tool.ToolResult{Content: result.Content, IsError: result.IsError}, nil
}
