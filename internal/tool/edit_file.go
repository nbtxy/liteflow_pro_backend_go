package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/liteflow/backend/internal/platform/storage"
)

type EditFileTool struct {
	storageSvc     storage.Service
	createArtifact CreateFileArtifactFunc
}

func NewEditFile(storageSvc storage.Service, createArtifact CreateFileArtifactFunc) *EditFileTool {
	return &EditFileTool{storageSvc: storageSvc, createArtifact: createArtifact}
}

func (t *EditFileTool) Name() string        { return "edit_file" }
func (t *EditFileTool) Description() string  { return "编辑对话中已有文件的部分内容，通过查找替换方式修改" }
func (t *EditFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要编辑的文件路径",
			},
			"old_content": map[string]any{
				"type":        "string",
				"description": "要替换的原内容",
			},
			"new_content": map[string]any{
				"type":        "string",
				"description": "替换后的新内容",
			},
		},
		"required": []string{"path", "old_content", "new_content"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)
	oldContent, _ := input["old_content"].(string)
	newContent, _ := input["new_content"].(string)

	if path == "" {
		return &ToolResult{Content: "path is required", IsError: true}, nil
	}
	if err := validateFilePath(path); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}
	if oldContent == "" {
		return &ToolResult{Content: "old_content is required", IsError: true}, nil
	}

	convID := tc.ConversationID.String()

	data, err := t.storageSvc.ReadFile(ctx, convID, path)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("读取文件失败: %v", err), IsError: true}, nil
	}

	current := string(data)
	if !strings.Contains(current, oldContent) {
		return &ToolResult{Content: "未找到要替换的内容", IsError: true}, nil
	}

	updated := strings.Replace(current, oldContent, newContent, 1)

	if err := t.storageSvc.UploadFile(ctx, convID, path, []byte(updated)); err != nil {
		return &ToolResult{Content: fmt.Sprintf("写入文件失败: %v", err), IsError: true}, nil
	}

	metadata, err := t.createArtifact(ctx, tc.ConversationID, tc.MessageID, path, updated)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("创建 artifact 失败: %v", err), IsError: true}, nil
	}

	return &ToolResult{
		Content:  fmt.Sprintf("文件已编辑: %s", path),
		Metadata: metadata,
	}, nil
}
