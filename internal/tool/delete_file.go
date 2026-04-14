package tool

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type DeleteByPathFunc func(ctx context.Context, conversationID uuid.UUID, filePath string) error

type DeleteFileTool struct {
	deleteFn DeleteByPathFunc
}

func NewDeleteFile(deleteFn DeleteByPathFunc) *DeleteFileTool {
	return &DeleteFileTool{deleteFn: deleteFn}
}

func (t *DeleteFileTool) Name() string        { return "delete_file" }
func (t *DeleteFileTool) Description() string  { return "删除对话中的指定文件" }
func (t *DeleteFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要删除的文件路径",
			},
		},
		"required": []string{"path"},
	}
}

func (t *DeleteFileTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)
	if path == "" {
		return &ToolResult{Content: "path is required", IsError: true}, nil
	}
	if err := validateFilePath(path); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	if err := t.deleteFn(ctx, tc.ConversationID, path); err != nil {
		return &ToolResult{Content: fmt.Sprintf("删除文件失败: %v", err), IsError: true}, nil
	}

	return &ToolResult{Content: fmt.Sprintf("文件已删除: %s", path)}, nil
}
