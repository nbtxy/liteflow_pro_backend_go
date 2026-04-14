package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/platform/storage"
)

type ViewTool struct {
	storageSvc storage.Service
	listFn     func(ctx context.Context, conversationID uuid.UUID) ([]domain.Artifact, error)
}

func NewView(storageSvc storage.Service, listFn func(ctx context.Context, conversationID uuid.UUID) ([]domain.Artifact, error)) *ViewTool {
	return &ViewTool{storageSvc: storageSvc, listFn: listFn}
}

func (t *ViewTool) Name() string        { return "view" }
func (t *ViewTool) Description() string  { return "查看对话中的文件内容，或不传 path 列出所有文件" }
func (t *ViewTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径；不传则列出所有文件",
			},
		},
	}
}

func (t *ViewTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)

	if path == "" {
		return t.listFiles(ctx, tc)
	}
	return t.readFile(ctx, path, tc)
}

func (t *ViewTool) listFiles(ctx context.Context, tc *ToolContext) (*ToolResult, error) {
	artifacts, err := t.listFn(ctx, tc.ConversationID)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("列出文件失败: %v", err), IsError: true}, nil
	}

	if len(artifacts) == 0 {
		return &ToolResult{Content: "当前对话中没有文件"}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("共 %d 个文件:\n", len(artifacts)))
	for _, a := range artifacts {
		size := "unknown"
		if a.FileSize != nil {
			size = fmt.Sprintf("%d bytes", *a.FileSize)
		}
		sb.WriteString(fmt.Sprintf("- %s (v%d, %s)\n", a.FilePath, a.Version, size))
	}

	return &ToolResult{Content: sb.String()}, nil
}

func (t *ViewTool) readFile(ctx context.Context, path string, tc *ToolContext) (*ToolResult, error) {
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
