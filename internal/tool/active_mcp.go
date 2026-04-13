package tool

import (
	"context"
	"fmt"
	"strings"
)

type McpActivateByPlatformFunc func(ctx context.Context, userID fmt.Stringer, platform string) ([]string, error)

type ActiveMCPTool struct {
	activateFn McpActivateByPlatformFunc
}

func NewActiveMCP(activateFn McpActivateByPlatformFunc) *ActiveMCPTool {
	return &ActiveMCPTool{activateFn: activateFn}
}

func (t *ActiveMCPTool) Name() string { return "active_mcp_tool" }
func (t *ActiveMCPTool) Description() string {
	return "按平台激活 MCP 工具。激活后仅保留 MCP 工具与 inactive_mcp_tool。"
}

func (t *ActiveMCPTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"platform": map[string]any{
				"type":        "string",
				"description": "平台名称，例如 github、cloudflare、notion、feishu",
			},
		},
		"required": []string{"platform"},
	}
}

func (t *ActiveMCPTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	platform, _ := input["platform"].(string)
	if platform == "" {
		return &ToolResult{Content: "platform 参数不能为空", IsError: true}, nil
	}

	if t.activateFn == nil {
		return &ToolResult{Content: "MCP service not available", IsError: true}, nil
	}

	activated, err := t.activateFn(ctx, tc.UserID, platform)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("activation failed: %v", err), IsError: true}, nil
	}

	if len(activated) == 0 {
		return &ToolResult{
			Content: fmt.Sprintf("未找到平台 '%s' 对应的 MCP 工具。", platform),
			Metadata: map[string]any{
				"mcp_mode":        "inactive",
				"activated_tools": []string{},
				"platform":        platform,
			},
		}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "已激活平台 %s 的 MCP 工具：\n", platform)
	for _, name := range activated {
		fmt.Fprintf(&sb, "- %s\n", name)
	}
	sb.WriteString("当前仅保留 MCP 工具与 inactive_mcp_tool。")

	return &ToolResult{
		Content: sb.String(),
		Metadata: map[string]any{
			"mcp_mode":        "active",
			"activated_tools": activated,
			"platform":        platform,
		},
	}, nil
}
