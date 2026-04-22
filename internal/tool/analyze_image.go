package tool

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/platform/storage"
)

type AnalyzeImageTool struct {
	storageSvc      storage.Service
	ossLinkSvc      storage.Service
	providerRouter  *llm.ProviderRouter
	defaultProvider string
	defaultModel    string
}

func NewAnalyzeImage(storageSvc storage.Service, ossLinkSvc storage.Service, providerRouter *llm.ProviderRouter) *AnalyzeImageTool {
	return &AnalyzeImageTool{
		storageSvc:      storageSvc,
		ossLinkSvc:      ossLinkSvc,
		providerRouter:  providerRouter,
		defaultProvider: "qwen",
		defaultModel:    "qwen3.6-plus",
	}
}

func (t *AnalyzeImageTool) Name() string { return "analyze_image" }

func (t *AnalyzeImageTool) Description() string {
	return "识别并分析对话中的图片内容（基于视觉模型）"
}

func (t *AnalyzeImageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "对话内图片路径（例如 uploads/example.png）",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "可选分析指令；不传则默认描述图片内容",
			},
		},
		"required": []string{"path"},
	}
}

func (t *AnalyzeImageTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	path, _ := input["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return &ToolResult{Content: "path is required", IsError: true}, nil
	}
	if err := validateFilePath(path); err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "请详细识别并描述这张图片中的主要内容、场景、文字与可疑风险点。"
	}

	data, err := t.storageSvc.ReadFile(ctx, tc.ConversationID.String(), path)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("读取图片失败: %v", err), IsError: true}, nil
	}

	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return &ToolResult{Content: fmt.Sprintf("文件不是图片: %s", mimeType), IsError: true}, nil
	}

	provider, err := t.providerRouter.Get(t.defaultProvider)
	if err != nil || provider == nil {
		return &ToolResult{Content: "视觉模型不可用：qwen provider 未配置", IsError: true}, nil
	}
	if !provider.SupportsVision() {
		return &ToolResult{Content: "当前视觉模型不支持图片输入", IsError: true}, nil
	}

	imageURL, err := t.presignedURLForAnalyze(ctx, tc.ConversationID.String(), path, data)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("生成图片临时链接失败: %v", err), IsError: true}, nil
	}

	req := &llm.LlmRequest{
		Messages: []llm.LlmMessage{
			{
				Role:    "user",
				Content: prompt,
				Images: []llm.ImageContent{
					{
						Type: "image_url",
						ImageURL: &llm.ImageURL{
							URL: imageURL,
						},
					},
				},
			},
		},
		Model:     t.defaultModel,
		MaxTokens: 2000,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("图片识别失败: %v", err), IsError: true}, nil
	}

	return &ToolResult{
		Content: strings.TrimSpace(resp.Content),
	}, nil
}

func (t *AnalyzeImageTool) presignedURLForAnalyze(ctx context.Context, conversationID, path string, data []byte) (string, error) {
	if t.ossLinkSvc == nil {
		return "", fmt.Errorf("OSS link storage 未配置")
	}
	if err := t.ossLinkSvc.UploadFile(ctx, conversationID, path, data); err != nil {
		return "", fmt.Errorf("上传图片到 OSS 失败: %w", err)
	}
	u, err := t.ossLinkSvc.GeneratePresignedURL(ctx, conversationID, path, "GET", 60)
	if err != nil {
		return "", fmt.Errorf("生成 OSS 临时链接失败: %w", err)
	}
	if u == "" {
		return "", fmt.Errorf("生成的 OSS 临时链接为空")
	}
	return u, nil
}
