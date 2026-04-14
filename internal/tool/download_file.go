package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/liteflow/backend/internal/platform/storage"
)

type DownloadFileTool struct {
	storageSvc     storage.Service
	createArtifact CreateFileArtifactFunc
}

func NewDownloadFile(storageSvc storage.Service, createArtifact CreateFileArtifactFunc) *DownloadFileTool {
	return &DownloadFileTool{storageSvc: storageSvc, createArtifact: createArtifact}
}

func (t *DownloadFileTool) Name() string { return "download_file" }
func (t *DownloadFileTool) Description() string {
	return "从 URL 下载文件并保存到当前对话中"
}
func (t *DownloadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "文件下载链接",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "保存的文件名；不传则从 URL 自动提取",
			},
		},
		"required": []string{"url"},
	}
}

const maxDownloadSize = 10 * 1024 * 1024 // 10MB

func (t *DownloadFileTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	rawURL, _ := input["url"].(string)
	filename, _ := input["filename"].(string)

	if rawURL == "" {
		return &ToolResult{Content: "url is required", IsError: true}, nil
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	// 从 URL 提取文件名
	if filename == "" {
		filename = path.Base(rawURL)
		// 去掉查询参数
		if idx := strings.IndexByte(filename, '?'); idx != -1 {
			filename = filename[:idx]
		}
		if filename == "" || filename == "." || filename == "/" {
			filename = "downloaded_file"
		}
	}
	if err := validateFilePath(filename); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("无效的 URL: %v", err), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LiteFlow/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("下载失败: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ToolResult{Content: fmt.Sprintf("下载失败，HTTP 状态码: %d", resp.StatusCode), IsError: true}, nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("读取响应失败: %v", err), IsError: true}, nil
	}
	if len(data) > maxDownloadSize {
		return &ToolResult{Content: fmt.Sprintf("文件超过大小限制 (%d MB)", maxDownloadSize/1024/1024), IsError: true}, nil
	}

	convID := tc.ConversationID.String()
	if err := t.storageSvc.UploadFile(ctx, convID, filename, data); err != nil {
		return &ToolResult{Content: fmt.Sprintf("保存文件失败: %v", err), IsError: true}, nil
	}

	metadata, err := t.createArtifact(ctx, tc.ConversationID, tc.MessageID, filename, string(data))
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("创建 artifact 失败: %v", err), IsError: true}, nil
	}

	return &ToolResult{
		Content:  fmt.Sprintf("文件已下载: %s (%d bytes)", filename, len(data)),
		Metadata: metadata,
	}, nil
}
