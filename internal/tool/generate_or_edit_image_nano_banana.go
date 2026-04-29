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

type imageGenerateBase struct {
	storageSvc       storage.Service
	ossLinkSvc       storage.Service // optional: when set, public tool reply uses OSS presigned HTTPS URLs
	createArtifact   CreateImageArtifactFunc
	recordUsage      RecordToolUsageFunc
	providerRouter   *imagegen.ProviderRouter
	toolName         string
	description      string
	providerName     string
	modelName        string
	useReferenceURLs bool // when true, fetch reference images as OSS presigned URLs instead of downloading bytes
}

func (t *imageGenerateBase) inputSchema() map[string]any {
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
				"enum":        []string{"512", "1K", "2K"},
				"description": "可选输出分辨率（Gemini image_size），最大支持 2K（512/1K/2K）",
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

func (t *imageGenerateBase) execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
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

	selectedProviderName := t.providerName
	selectedModel := t.modelName
	selectedProvider, getErr := t.providerRouter.Get(selectedProviderName)
	if getErr != nil {
		return &ToolResult{Content: getErr.Error(), IsError: true}, nil
	}
	if selectedProvider == nil {
		return &ToolResult{Content: fmt.Sprintf("image provider unavailable: %s", selectedProviderName), IsError: true}, nil
	}

	t.emitNarration(ctx, tc, "已接收图像任务，开始准备输入...")
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
	t.emitNarration(ctx, tc, "参考图已就绪，正在请求模型生成...")

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
	if metadata == nil {
		metadata = make(map[string]any)
	}
	t.recordToolUsage(ctx, tc, resp.Usage)

	const presignExpireMinutes = 60
	signedURL, signErr, usedOSS := t.presignedURLForGeneratedImage(ctx, tc.ConversationID.String(), filename, imageBytes, presignExpireMinutes)
	metadata["provider"] = selectedProvider.Name()
	metadata["model"] = selectedModel

	content := fmt.Sprintf(
		"SUCCESS: image generated and saved as %s (%d bytes). The image is delivered to the user automatically. Do not call this tool again unless the user asks for changes.",
		filename,
		len(imageBytes),
	)
	if signErr != nil {
		slog.Warn("generate presigned url failed", "path", filename, "usedOSS", usedOSS, "err", signErr)
	} else {
		expiresAt := time.Now().Add(presignExpireMinutes * time.Minute)
		metadata["oss_url"] = signedURL
		metadata["download_url"] = signedURL
		metadata["oss_url_expires_at"] = expiresAt.Format(time.RFC3339)
		metadata["oss_url_expires_in_minutes"] = presignExpireMinutes
		metadata["download_url_expires_at"] = expiresAt.Format(time.RFC3339)
		metadata["download_url_expires_in_minutes"] = presignExpireMinutes
		if usedOSS {
			metadata["oss_link_source"] = "aliyun_oss"
			metadata["download_url_label"] = "OSS 临时链接"
		} else {
			metadata["download_url_label"] = "临时链接"
		}
	}

	return &ToolResult{
		Content:  content,
		Metadata: metadata,
	}, nil
}

// presignedURLForGeneratedImage mirrors the image to OSS when ossLinkSvc is set and returns an Aliyun HTTPS presigned URL; on OSS failure falls back to storageSvc (e.g. local /api/files path).
func (t *imageGenerateBase) presignedURLForGeneratedImage(ctx context.Context, conversationID, filename string, imageBytes []byte, expireMinutes int) (string, error, bool) {
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

func (t *imageGenerateBase) emitNarration(ctx context.Context, tc *ToolContext, content string) {
	toolUseID := ""
	if tc != nil {
		toolUseID = tc.ToolUseID
	}
	events.Emit(ctx, events.NewEvent("tool_progress", map[string]any{
		"toolUseId": toolUseID,
		"toolName":  t.toolName,
		"content":   content,
	}))
}

func (t *imageGenerateBase) recordToolUsage(ctx context.Context, tc *ToolContext, usage *llm.LlmUsage) {
	if t.recordUsage == nil || tc == nil || usage == nil || usage.TotalTokens() <= 0 {
		return
	}
	if tc.UserID == uuid.Nil || tc.ConversationID == uuid.Nil || tc.MessageID == uuid.Nil {
		return
	}
	t.recordUsage(ctx, tc.UserID, tc.ConversationID, tc.MessageID, usage)
}

func (t *imageGenerateBase) fetchReferenceImageByPath(ctx context.Context, conversationID, filePath string) (imagegen.InputImage, error) {
	if t.useReferenceURLs && t.ossLinkSvc != nil {
		url, err := t.ossLinkSvc.GeneratePresignedURL(ctx, conversationID, filePath, "GET", 30)
		if err != nil {
			return imagegen.InputImage{}, fmt.Errorf("presign reference image: %w", err)
		}
		return imagegen.InputImage{URL: url}, nil
	}
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

type NanoBananaImageGenerateTool struct {
	base imageGenerateBase
}

func NewNanoBananaImageGenerate(
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	createArtifact CreateImageArtifactFunc,
	recordUsage RecordToolUsageFunc,
	providerRouter *imagegen.ProviderRouter,
	modelName string,
) *NanoBananaImageGenerateTool {
	return &NanoBananaImageGenerateTool{
		base: imageGenerateBase{
			storageSvc:     storageSvc,
			ossLinkSvc:     ossLinkSvc,
			createArtifact: createArtifact,
			recordUsage:    recordUsage,
			providerRouter: providerRouter,
			toolName:       "generate_or_edit_image_nano_banana",
			description:    "生成或编辑图像：固定使用 Nano Banana（Cloudflare Gemini Image）模型链路；仅给 prompt 时从零生成，传入 reference_image_paths 时将其作为原图进行编辑/修改/风格迁移（结果以 IMAGE artifact 返回）",
			providerName:   "cloudflare-gemini-image",
			modelName:      strings.TrimSpace(modelName),
		},
	}
}

func (t *NanoBananaImageGenerateTool) Name() string { return t.base.toolName }

func (t *NanoBananaImageGenerateTool) Description() string { return t.base.description }

func (t *NanoBananaImageGenerateTool) InputSchema() map[string]any { return t.base.inputSchema() }

func (t *NanoBananaImageGenerateTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	return t.base.execute(ctx, input, tc)
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

	upper := strings.ToUpper(s)
	switch upper {
	case "512", "1K", "2K":
		return upper, true, nil
	case "4K":
		// Cap output resolution at 2K for stability/cost control.
		return "2K", true, nil
	// backward compatibility for legacy resolution values.
	case "512X512":
		return "512", true, nil
	case "768X768", "1024X1024":
		return "1K", true, nil
	case "1024X1536", "1536X1024":
		return "2K", true, nil
	default:
		return "", false, fmt.Errorf("resolution must be one of: 512, 1K, 2K")
	}
}
