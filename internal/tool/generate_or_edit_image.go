package tool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/imagegen"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/platform/events"
	"github.com/liteflow/backend/internal/platform/storage"
)

type RecordToolUsageFunc func(ctx context.Context, userID, conversationID, messageID uuid.UUID, usage *llm.LlmUsage)

type ImageGenerateTool struct {
	storageSvc      storage.Service
	ossLinkSvc      storage.Service // optional: when set, public tool reply uses OSS presigned HTTPS URLs
	createArtifact  CreateImageArtifactFunc
	recordUsage     RecordToolUsageFunc
	providerRouter  *imagegen.ProviderRouter
	defaultProvider string
	defaultModel    string
}

func NewImageGenerate(
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	createArtifact CreateImageArtifactFunc,
	recordUsage RecordToolUsageFunc,
	providerRouter *imagegen.ProviderRouter,
	defaultProvider string,
	defaultModel string,
) *ImageGenerateTool {
	defaultProvider = strings.TrimSpace(defaultProvider)
	defaultModel = strings.TrimSpace(defaultModel)
	return &ImageGenerateTool{
		storageSvc:      storageSvc,
		ossLinkSvc:      ossLinkSvc,
		createArtifact:  createArtifact,
		recordUsage:     recordUsage,
		providerRouter:  providerRouter,
		defaultProvider: defaultProvider,
		defaultModel:    defaultModel,
	}
}

func (t *ImageGenerateTool) Name() string { return "generate_or_edit_image" }

func (t *ImageGenerateTool) Description() string {
	return "生成或编辑图像：仅给 prompt 时从零生成；传入 reference_image_paths 时将其作为原图进行编辑/修改/风格迁移（通过调用 Google Nano Banana 2，结果以 IMAGE artifact 返回）"
}

func (t *ImageGenerateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "生成或编辑指令：新建图像时描述画面；编辑图像时描述要做的修改（如『把背景换成海边』『去掉左上角的文字』）",
			},
			"aspect_ratio": map[string]any{
				"type":        "string",
				"enum":        []string{"1:1", "16:9", "9:16", "4:3", "3:4"},
				"description": "可选宽高比，默认由模型决定（编辑原图时一般不传，避免改变构图）",
			},
			"image_count": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     4,
				"description": "可选生成图片数量（1-4），默认由模型或网关决定",
			},
			"resolution": map[string]any{
				"type":        "string",
				"enum":        []string{"512x512", "768x768", "1024x1024", "1024x1536", "1536x1024"},
				"description": "可选输出分辨率（宽x高）",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "可选图片生成 provider 名称，不传则使用默认 provider",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "可选图片生成模型名，不传则使用默认模型",
			},
			"reference_image_paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "可选原图/参考图文件路径（对话内路径，如 uploads/xxx.png）。服务端会直接读取文件并作为参考图输入模型",
			},
		},
		"required": []string{"prompt"},
	}
}

type geminiPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *geminiInlineIn `json:"inlineData,omitempty"`
}

type geminiInlineIn struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiChunk struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
}

func (t *ImageGenerateTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return &ToolResult{Content: "prompt is required", IsError: true}, nil
	}
	if t.providerRouter == nil {
		return &ToolResult{Content: "image generation provider router not configured", IsError: true}, nil
	}

	aspectRatio, _ := input["aspect_ratio"].(string)
	aspectRatio = strings.TrimSpace(aspectRatio)

	imageCount, hasImageCount, err := parseImageCount(input["image_count"])
	if err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	resolution, hasResolution, err := parseResolution(input["resolution"])
	if err != nil {
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	selectedProviderName := t.defaultProvider
	if v, ok := input["provider"].(string); ok && strings.TrimSpace(v) != "" {
		selectedProviderName = strings.TrimSpace(v)
	}
	selectedModel := t.defaultModel
	if v, ok := input["model"].(string); ok && strings.TrimSpace(v) != "" {
		selectedModel = strings.TrimSpace(v)
	}

	selectedProvider := t.providerRouter.Default()
	if selectedProviderName != "" {
		p, getErr := t.providerRouter.Get(selectedProviderName)
		if getErr != nil {
			return &ToolResult{Content: getErr.Error(), IsError: true}, nil
		}
		selectedProvider = p
	}
	if selectedProvider == nil {
		return &ToolResult{Content: "no image generation provider available", IsError: true}, nil
	}

	t.emitNarration(ctx, "已接收图像任务，开始准备输入...")
	referenceImages := make([]imagegen.InputImage, 0, 4)
	if refs, ok := input["reference_image_paths"]; ok {
		for _, filePath := range toStringSlice(refs) {
			if filePath == "" {
				continue
			}
			ref, err := t.fetchReferenceImageByPath(ctx, tc.ConversationID.String(), filePath)
			if err != nil {
				slog.Warn("reference image fetch by path failed", "path", filePath, "err", err)
				continue
			}
			referenceImages = append(referenceImages, ref)
		}
	}
	t.emitNarration(ctx, "参考图已就绪，正在请求模型生成...")

	providerReq := &imagegen.Request{
		Prompt:          prompt,
		AspectRatio:     aspectRatio,
		Model:           selectedModel,
		ReferenceImages: referenceImages,
	}
	if hasImageCount {
		providerReq.ImageCount = imageCount
	}
	if hasResolution {
		providerReq.Resolution = resolution
	}
	resp, err := selectedProvider.Generate(ctx, providerReq, func(delta string) {
		events.Emit(ctx, events.NewEvent("text_delta", map[string]any{
			"content": delta,
			"source":  "tool:generate_or_edit_image",
		}))
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return &ToolResult{Content: "image generation cancelled", IsError: true}, nil
		}
		return &ToolResult{Content: fmt.Sprintf("image generation failed: %v", err), IsError: true}, nil
	}
	if resp == nil || len(resp.ImageBytes) == 0 {
		return &ToolResult{Content: "no image data returned", IsError: true}, nil
	}

	mimeType := resp.MimeType
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "image/png"
	}
	imageBytes := resp.ImageBytes

	filename := fmt.Sprintf("generated_%d%s", time.Now().UnixNano(), extFromMime(mimeType))
	if err := t.storageSvc.UploadFile(ctx, tc.ConversationID.String(), filename, imageBytes); err != nil {
		return &ToolResult{Content: fmt.Sprintf("save image failed: %v", err), IsError: true}, nil
	}

	metadata, err := t.createArtifact(ctx, tc.ConversationID, tc.MessageID, filename, imageBytes, mimeType)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("create artifact failed: %v", err), IsError: true}, nil
	}
	t.recordToolUsage(ctx, tc, resp.Usage)

	const presignExpireMinutes = 60
	signedURL, signErr, usedOSS := t.presignedURLForGeneratedImage(ctx, tc.ConversationID.String(), filename, imageBytes, presignExpireMinutes)
	content := fmt.Sprintf("图像已生成: %s (%d bytes)", filename, len(imageBytes))
	if signErr != nil {
		slog.Warn("generate presigned url failed", "path", filename, "usedOSS", usedOSS, "err", signErr)
	} else {
		expiresAt := time.Now().Add(presignExpireMinutes * time.Minute)
		metadata["provider"] = selectedProvider.Name()
		metadata["model"] = selectedModel
		metadata["oss_url"] = signedURL
		metadata["oss_url_expires_at"] = expiresAt.Format(time.RFC3339)
		metadata["oss_url_expires_in_minutes"] = presignExpireMinutes
		if usedOSS {
			metadata["oss_link_source"] = "aliyun_oss"
		}
		linkLabel := "临时链接"
		if usedOSS {
			linkLabel = "OSS 临时链接"
		}
		content = fmt.Sprintf(
			"图像已生成: %s (%d bytes)\n%s: %s\n链接有效期: %d 分钟（有效期至 %s）",
			filename,
			len(imageBytes),
			linkLabel,
			signedURL,
			presignExpireMinutes,
			expiresAt.Local().Format("2006-01-02 15:04:05 MST"),
		)
	}

	return &ToolResult{
		Content:  content,
		Metadata: metadata,
	}, nil
}

// presignedURLForGeneratedImage mirrors the image to OSS when ossLinkSvc is set and returns an Aliyun HTTPS presigned URL; on OSS failure falls back to storageSvc (e.g. local /api/files path).
func (t *ImageGenerateTool) presignedURLForGeneratedImage(ctx context.Context, conversationID, filename string, imageBytes []byte, expireMinutes int) (string, error, bool) {
	if t.ossLinkSvc != nil {
		if err := t.ossLinkSvc.UploadFile(ctx, conversationID, filename, imageBytes); err != nil {
			slog.Warn("upload generated image to OSS failed, using app storage URL", "path", filename, "err", err)
		} else {
			u, err := t.ossLinkSvc.GeneratePresignedURL(ctx, conversationID, filename, "GET", expireMinutes)
			if err == nil && u != "" {
				return u, nil, true
			}
			slog.Warn("OSS presign failed, using app storage URL", "path", filename, "err", err)
		}
	}
	u, err := t.storageSvc.GeneratePresignedURL(ctx, conversationID, filename, "GET", expireMinutes)
	return u, err, false
}

func (t *ImageGenerateTool) emitNarration(ctx context.Context, content string) {
	events.Emit(ctx, events.NewEvent("text_delta", map[string]any{
		"content": content,
		"source":  "tool:generate_or_edit_image",
	}))
}

func (t *ImageGenerateTool) recordToolUsage(ctx context.Context, tc *ToolContext, usage *llm.LlmUsage) {
	if t.recordUsage == nil || tc == nil || usage == nil || usage.TotalTokens() <= 0 {
		return
	}
	if tc.UserID == uuid.Nil || tc.ConversationID == uuid.Nil || tc.MessageID == uuid.Nil {
		return
	}
	t.recordUsage(ctx, tc.UserID, tc.ConversationID, tc.MessageID, usage)
}

func extFromMime(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func toStringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		res := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				res = append(res, s)
			}
		}
		return res
	default:
		return nil
	}
}

func parseImageCount(v any) (int, bool, error) {
	if v == nil {
		return 0, false, nil
	}

	var count int
	switch vv := v.(type) {
	case int:
		count = vv
	case int32:
		count = int(vv)
	case int64:
		count = int(vv)
	case float64:
		// JSON number is usually float64.
		if vv != float64(int(vv)) {
			return 0, false, fmt.Errorf("image_count must be an integer")
		}
		count = int(vv)
	default:
		return 0, false, fmt.Errorf("image_count must be an integer")
	}

	if count < 1 || count > 4 {
		return 0, false, fmt.Errorf("image_count must be between 1 and 4")
	}
	return count, true, nil
}

func parseResolution(v any) (string, bool, error) {
	if v == nil {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("resolution must be a string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false, nil
	}
	switch s {
	case "512x512", "768x768", "1024x1024", "1024x1536", "1536x1024":
		return s, true, nil
	default:
		return "", false, fmt.Errorf("resolution must be one of: 512x512, 768x768, 1024x1024, 1024x1536, 1536x1024")
	}
}

func (t *ImageGenerateTool) fetchReferenceImageByPath(ctx context.Context, conversationID, filePath string) (imagegen.InputImage, error) {
	data, err := t.storageSvc.ReadFile(ctx, conversationID, filePath)
	if err != nil {
		return imagegen.InputImage{}, fmt.Errorf("read file by path: %w", err)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return imagegen.InputImage{}, fmt.Errorf("not an image: %s", mime)
	}
	return imagegen.InputImage{
		MimeType: mime,
		Data:     data,
	}, nil
}
