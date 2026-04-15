package tool

import (
	"context"
	"fmt"
	"path"
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

func (t *ViewTool) Name() string { return "view" }
func (t *ViewTool) Description() string {
	return "查看对话中的文件内容（支持按文件路径或文件名），或不传 path 列出所有文件"
}
func (t *ViewTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径或文件名；不传则列出所有文件",
			},
		},
	}
}

func (t *ViewTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	pathOrName, _ := input["path"].(string)

	if pathOrName == "" {
		return t.listFiles(ctx, tc)
	}
	return t.readFile(ctx, pathOrName, tc)
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

func (t *ViewTool) readFile(ctx context.Context, pathOrName string, tc *ToolContext) (*ToolResult, error) {
	resolvedPath, err := t.resolveFilePath(ctx, pathOrName, tc)
	if err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	err = validateFilePath(resolvedPath)
	if err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	data, err := t.storageSvc.ReadFile(ctx, tc.ConversationID.String(), resolvedPath)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("读取文件失败: %v", err), IsError: true}, nil
	}

	content := string(data)

	return &ToolResult{Content: content}, nil
}

func (t *ViewTool) resolveFilePath(ctx context.Context, pathOrName string, tc *ToolContext) (string, error) {
	normalizeInput := func(s string) string {
		s = strings.TrimSpace(s)
		// 容错：LLM 或用户有时会把文件名包在引号/反引号里。
		s = strings.Trim(s, "`\"'")
		s = strings.ReplaceAll(s, "\\", "/")
		return strings.TrimPrefix(s, "./")
	}

	query := normalizeInput(pathOrName)
	if query == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}

	artifacts, err := t.listFn(ctx, tc.ConversationID)
	if err != nil {
		return "", fmt.Errorf("列出文件失败: %v", err)
	}

	type match struct {
		Path string
		Kind string
	}

	addMatch := func(dst []match, path, kind string) []match {
		for _, m := range dst {
			if m.Path == path {
				return dst
			}
		}
		return append(dst, match{Path: path, Kind: kind})
	}

	var pathMatches []match
	var titleMatches []match
	var nameMatches []match

	for _, a := range artifacts {
		artifactPath := normalizeInput(a.FilePath)
		if artifactPath == query {
			pathMatches = addMatch(pathMatches, a.FilePath, "path")
		}

		title := ""
		if a.Title != nil {
			title = normalizeInput(*a.Title)
		}
		if title != "" && title == query {
			titleMatches = addMatch(titleMatches, a.FilePath, "title")
		}

		if !strings.Contains(query, "/") {
			if path.Base(artifactPath) == query || (title != "" && path.Base(title) == query) {
				nameMatches = addMatch(nameMatches, a.FilePath, "name")
			}
		}
	}

	switch {
	case len(pathMatches) == 1:
		return pathMatches[0].Path, nil
	case len(pathMatches) > 1:
		paths := make([]string, 0, len(pathMatches))
		for _, m := range pathMatches {
			paths = append(paths, m.Path)
		}
		return "", fmt.Errorf("存在多个路径匹配文件，请使用更精确路径:\n- %s", strings.Join(paths, "\n- "))
	case len(titleMatches) == 1:
		return titleMatches[0].Path, nil
	case len(titleMatches) > 1:
		paths := make([]string, 0, len(titleMatches))
		for _, m := range titleMatches {
			paths = append(paths, m.Path)
		}
		return "", fmt.Errorf("存在多个标题匹配文件，请使用完整路径:\n- %s", strings.Join(paths, "\n- "))
	case len(nameMatches) == 1:
		return nameMatches[0].Path, nil
	case len(nameMatches) > 1:
		paths := make([]string, 0, len(nameMatches))
		for _, m := range nameMatches {
			paths = append(paths, m.Path)
		}
		return "", fmt.Errorf("存在多个同名文件，请使用完整路径:\n- %s", strings.Join(paths, "\n- "))
	}

	candidates := make([]string, 0, len(artifacts)*2)
	seen := make(map[string]struct{}, len(artifacts)*2)
	for _, a := range artifacts {
		artifactPath := normalizeInput(a.FilePath)
		artifactBase := path.Base(artifactPath)
		if artifactBase != "." && artifactBase != "/" && artifactBase != "" {
			if _, ok := seen[artifactBase]; !ok {
				seen[artifactBase] = struct{}{}
				candidates = append(candidates, artifactBase)
			}
		}
		if a.Title != nil {
			title := normalizeInput(*a.Title)
			titleBase := path.Base(title)
			if titleBase != "." && titleBase != "/" && titleBase != "" {
				if _, ok := seen[titleBase]; !ok {
					seen[titleBase] = struct{}{}
					candidates = append(candidates, titleBase)
				}
			}
		}
	}
	if len(candidates) > 0 {
		return "", fmt.Errorf("文件不存在: %s\n可用文件名或标题: %s", query, strings.Join(candidates, ", "))
	}
	return "", fmt.Errorf("文件不存在: %s", query)
}
