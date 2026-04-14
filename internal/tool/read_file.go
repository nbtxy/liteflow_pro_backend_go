package tool

import (
	"context"
	"fmt"

	"github.com/liteflow/backend/internal/platform/storage"
)

type ReadFileTool struct {
	storageSvc storage.Service
}

func NewReadFile(storageSvc storage.Service) *ReadFileTool {
	return &ReadFileTool{storageSvc: storageSvc}
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string  { return "读取对话中已有文件的内容" }
func (t *ReadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要读取的文件路径",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)
	if path == "" {
		return &ToolResult{Content: "path is required", IsError: true}, nil
	}
	if err := validateFilePath(path); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	data, err := t.storageSvc.ReadFile(ctx, tc.ConversationID.String(), path)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("读取文件失败: %v", err), IsError: true}, nil
	}

	content := string(data)
	if len(content) > 3000 {
		content = content[:3000] + "\n...(内容已截断)"
	}

	return &ToolResult{Content: content}, nil
}
