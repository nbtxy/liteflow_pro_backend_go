package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/platform/storage"
)

type CreateFileArtifactFunc func(ctx context.Context, conversationID, messageID uuid.UUID, path, content string) (map[string]any, error)

type CreateFileTool struct {
	storageSvc      storage.Service
	createArtifact  CreateFileArtifactFunc
}

func NewCreateFile(storageSvc storage.Service, createArtifact CreateFileArtifactFunc) *CreateFileTool {
	return &CreateFileTool{storageSvc: storageSvc, createArtifact: createArtifact}
}

func (t *CreateFileTool) Name() string        { return "create_file" }
func (t *CreateFileTool) Description() string  { return "在对话中创建或覆盖文件，用于生成代码、文档等内容" }
func (t *CreateFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径，如 main.py、src/utils.js",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "文件内容",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *CreateFileTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)
	content, _ := input["content"].(string)

	if path == "" {
		return &ToolResult{Content: "path is required", IsError: true}, nil
	}
	if err := validateFilePath(path); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}
	if content == "" {
		return &ToolResult{Content: "content is required", IsError: true}, nil
	}

	convID := tc.ConversationID.String()
	if err := t.storageSvc.UploadFile(ctx, convID, path, []byte(content)); err != nil {
		return &ToolResult{Content: fmt.Sprintf("写入文件失败: %v", err), IsError: true}, nil
	}

	metadata, err := t.createArtifact(ctx, tc.ConversationID, tc.MessageID, path, content)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("创建 artifact 失败: %v", err), IsError: true}, nil
	}

	return &ToolResult{
		Content:  fmt.Sprintf("文件已创建: %s", path),
		Metadata: metadata,
	}, nil
}

// validateFilePath checks for path traversal and other unsafe patterns.
func validateFilePath(path string) error {
	if path == "" {
		return errors.New("文件路径不能为空")
	}
	if strings.HasPrefix(path, "/") {
		return errors.New("不允许使用绝对路径")
	}
	if strings.Contains(path, "..") {
		return errors.New("文件路径不允许包含 ..")
	}
	if strings.ContainsRune(path, 0) {
		return errors.New("文件路径包含非法字符")
	}
	return nil
}
