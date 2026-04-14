package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/domain"
)

type ListFilesFunc func(ctx context.Context, conversationID uuid.UUID) ([]domain.Artifact, error)

type ListFilesTool struct {
	listFn ListFilesFunc
}

func NewListFiles(listFn ListFilesFunc) *ListFilesTool {
	return &ListFilesTool{listFn: listFn}
}

func (t *ListFilesTool) Name() string        { return "list_files" }
func (t *ListFilesTool) Description() string  { return "列出当前对话中的所有文件" }
func (t *ListFilesTool) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListFilesTool) Execute(ctx context.Context, _ map[string]any, tc *ToolContext) (*ToolResult, error) {
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
