package tool

import (
	"context"
	"fmt"
)

type MemoryManageFunc func(ctx context.Context, userID fmt.Stringer, action string, input map[string]any) (string, error)

type MemoryManageTool struct {
	manageFn MemoryManageFunc
}

func NewMemoryManage(manageFn MemoryManageFunc) *MemoryManageTool {
	return &MemoryManageTool{manageFn: manageFn}
}

func (t *MemoryManageTool) Name() string { return "memory_manage" }
func (t *MemoryManageTool) Description() string {
	return "Manage user memories: add, update, or delete memory entries. Used to remember user preferences, project context, and important information."
}

func (t *MemoryManageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action: add, update, delete",
				"enum":        []string{"add", "update", "delete"},
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Memory category: IDENTITY, PREFERENCE, PROJECT, SKILL, CONTEXT, PERSONAL, INSTRUCTION",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Memory content",
			},
			"memory_id": map[string]any{
				"type":        "string",
				"description": "Memory ID (for update/delete)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MemoryManageTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	action, _ := input["action"].(string)
	if action == "" {
		return &ToolResult{Content: "action is required", IsError: true}, nil
	}

	if t.manageFn == nil {
		return &ToolResult{Content: "memory service not available", IsError: true}, nil
	}

	result, err := t.manageFn(ctx, tc.UserID, action, input)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("memory operation failed: %v", err), IsError: true}, nil
	}

	return &ToolResult{Content: result}, nil
}
