package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/liteflow/backend/internal/platform/storage"
)

type StrReplaceTool struct {
	storageSvc     storage.Service
	cacheStorage   storage.Service
	createArtifact CreateFileArtifactFunc
}

func NewStrReplace(storageSvc storage.Service, cacheStorage storage.Service, createArtifact CreateFileArtifactFunc) *StrReplaceTool {
	return &StrReplaceTool{storageSvc: storageSvc, cacheStorage: cacheStorage, createArtifact: createArtifact}
}

func (t *StrReplaceTool) Name() string { return "str_replace" }
func (t *StrReplaceTool) Description() string {
	return "编辑对话中已有文件，通过查找替换修改指定内容"
}
func (t *StrReplaceTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要编辑的文件路径",
			},
			"old_str": map[string]any{
				"type":        "string",
				"description": "要替换的原内容",
			},
			"new_str": map[string]any{
				"type":        "string",
				"description": "替换后的新内容",
			},
		},
		"required": []string{"path", "old_str", "new_str"},
	}
}

func (t *StrReplaceTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)
	oldStr, _ := input["old_str"].(string)
	newStr, _ := input["new_str"].(string)

	if path == "" {
		return &ToolResult{Content: "path is required", IsError: true}, nil
	}
	if err := validateFilePath(path); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}
	if oldStr == "" {
		return &ToolResult{Content: "old_str is required", IsError: true}, nil
	}

	convID := tc.ConversationID.String()

	data, err := t.storageSvc.ReadFile(ctx, convID, path)
	if err != nil {
		if t.cacheStorage != nil {
			cacheData, cacheErr := t.cacheStorage.ReadFile(ctx, convID, path)
			if cacheErr != nil {
				return &ToolResult{Content: fmt.Sprintf("读取文件失败: %v", err), IsError: true}, nil
			}
			data = cacheData
		} else {
			return &ToolResult{Content: fmt.Sprintf("读取文件失败: %v", err), IsError: true}, nil
		}
	}

	current := string(data)
	if !strings.Contains(current, oldStr) {
		return &ToolResult{Content: "未找到要替换的内容", IsError: true}, nil
	}

	updated := strings.Replace(current, oldStr, newStr, 1)

	if err := t.storageSvc.UploadFile(ctx, convID, path, []byte(updated)); err != nil {
		return &ToolResult{Content: fmt.Sprintf("写入文件失败: %v", err), IsError: true}, nil
	}
	if t.cacheStorage != nil {
		if err := t.cacheStorage.UploadFile(ctx, convID, path, []byte(updated)); err != nil {
			return &ToolResult{Content: fmt.Sprintf("写入缓存文件失败: %v", err), IsError: true}, nil
		}
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
