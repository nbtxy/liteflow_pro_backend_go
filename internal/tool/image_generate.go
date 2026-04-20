package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/llm"
	"github.com/liteflow/backend/internal/platform/events"
	"github.com/liteflow/backend/internal/platform/storage"
)

type RecordToolUsageFunc func(ctx context.Context, userID, conversationID, messageID uuid.UUID, usage *llm.LlmUsage)

type ImageGenerateTool struct {
	storageSvc     storage.Service
	ossLinkSvc     storage.Service // optional: when set, public tool reply uses OSS presigned HTTPS URLs
	createArtifact CreateImageArtifactFunc
	recordUsage    RecordToolUsageFunc
	httpClient     *http.Client
	cfEndpoint     string
	cfToken        string
	model          string
}

func NewImageGenerate(
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	createArtifact CreateImageArtifactFunc,
	recordUsage RecordToolUsageFunc,
	cfg config.CloudflareConfig,
) *ImageGenerateTool {
	model := strings.TrimSpace(cfg.ImageModel)
	if model == "" {
		model = "gemini-3.1-flash-image-preview"
	}
	return &ImageGenerateTool{
		storageSvc:     storageSvc,
		ossLinkSvc:     ossLinkSvc,
		createArtifact: createArtifact,
		recordUsage:    recordUsage,
		httpClient:     &http.Client{},
		cfEndpoint:     normalizeCloudflareGatewayBase(cfg.Endpoint),
		cfToken:        strings.TrimSpace(cfg.Token),
		model:          model,
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
	if t.cfEndpoint == "" || t.cfToken == "" {
		return &ToolResult{Content: "image generation not configured", IsError: true}, nil
	}

	aspectRatio, _ := input["aspect_ratio"].(string)

	t.emitNarration(ctx, "已接收图像任务，开始准备输入...")
	parts := []geminiPart{{Text: prompt}}
	if refs, ok := input["reference_image_paths"]; ok {
		for _, filePath := range toStringSlice(refs) {
			if filePath == "" {
				continue
			}
			refPart, err := t.fetchReferencePartFromPath(ctx, tc.ConversationID.String(), filePath)
			if err != nil {
				slog.Warn("reference image fetch by path failed", "path", filePath, "err", err)
				continue
			}
			parts = append(parts, refPart)
		}
	}
	t.emitNarration(ctx, "参考图已就绪，正在请求模型生成...")
	genConfig := map[string]any{
		"responseModalities": []string{"IMAGE", "TEXT"},
	}
	if aspectRatio != "" {
		genConfig["imageConfig"] = map[string]any{"aspectRatio": aspectRatio}
	}

	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": parts,
			},
		},
		"generationConfig": genConfig,
	})
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("marshal request failed: %v", err), IsError: true}, nil
	}

	url := fmt.Sprintf("%s/google-ai-studio/v1beta/models/%s:streamGenerateContent?alt=sse", t.cfEndpoint, t.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("build request failed: %v", err), IsError: true}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("cf-aig-authorization", "Bearer "+t.cfToken)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return &ToolResult{Content: "image generation cancelled", IsError: true}, nil
		}
		return &ToolResult{Content: fmt.Sprintf("request failed: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return &ToolResult{
			Content: fmt.Sprintf("image gateway error: %d %s", resp.StatusCode, strings.TrimSpace(string(respBody))),
			IsError: true,
		}, nil
	}

	var imageB64 strings.Builder
	mimeType := "image/png"
	usageAcc := &llm.LlmUsage{}
	reader := bufio.NewReader(resp.Body)
	emittedFirstChunk := false
	streamStart := time.Now()
	chunkCount := 0
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if payload == "" || payload == "[DONE]" {
					if readErr != nil {
						break
					}
					continue
				}
				var chunk geminiChunk
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					slog.Warn("gemini chunk parse failed", "err", err, "payload", truncateForLog(payload, 200))
				} else {
					if chunk.UsageMetadata != nil {
						if chunk.UsageMetadata.PromptTokenCount > usageAcc.InputTokens {
							usageAcc.InputTokens = chunk.UsageMetadata.PromptTokenCount
						}
						if chunk.UsageMetadata.CandidatesTokenCount > usageAcc.OutputTokens {
							usageAcc.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount
						}
					}
					chunkCount++
					slog.Info("gemini chunk",
						"idx", chunkCount,
						"ms_since_stream_start", time.Since(streamStart).Milliseconds(),
						"payload_bytes", len(payload),
					)
					if !emittedFirstChunk {
						emittedFirstChunk = true
						t.emitNarration(ctx, "模型已开始返回流式结果...")
					}
					for _, cand := range chunk.Candidates {
						for _, part := range cand.Content.Parts {
							if part.Text != "" {
								events.Emit(ctx, events.NewEvent("text_delta", map[string]any{
									"content": part.Text,
									"source":  "tool:generate_or_edit_image",
								}))
							}
							if part.InlineData != nil && part.InlineData.Data != "" {
								imageB64.WriteString(part.InlineData.Data)
								if part.InlineData.MimeType != "" {
									mimeType = part.InlineData.MimeType
								}
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return &ToolResult{Content: "image generation cancelled", IsError: true}, nil
			}
			return &ToolResult{Content: fmt.Sprintf("stream read failed: %v", readErr), IsError: true}, nil
		}
	}

	if imageB64.Len() == 0 {
		return &ToolResult{Content: "no image data returned", IsError: true}, nil
	}

	imageBytes, err := base64.StdEncoding.DecodeString(imageB64.String())
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("decode image failed: %v", err), IsError: true}, nil
	}

	filename := fmt.Sprintf("generated_%d%s", time.Now().UnixNano(), extFromMime(mimeType))
	if err := t.storageSvc.UploadFile(ctx, tc.ConversationID.String(), filename, imageBytes); err != nil {
		return &ToolResult{Content: fmt.Sprintf("save image failed: %v", err), IsError: true}, nil
	}

	metadata, err := t.createArtifact(ctx, tc.ConversationID, tc.MessageID, filename, imageBytes, mimeType)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("create artifact failed: %v", err), IsError: true}, nil
	}
	t.recordToolUsage(ctx, tc, usageAcc)

	const presignExpireMinutes = 60
	signedURL, signErr, usedOSS := t.presignedURLForGeneratedImage(ctx, tc.ConversationID.String(), filename, imageBytes, presignExpireMinutes)
	content := fmt.Sprintf("图像已生成: %s (%d bytes)", filename, len(imageBytes))
	if signErr != nil {
		slog.Warn("generate presigned url failed", "path", filename, "usedOSS", usedOSS, "err", signErr)
	} else {
		expiresAt := time.Now().Add(presignExpireMinutes * time.Minute)
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

func (t *ImageGenerateTool) fetchReferencePartFromPath(ctx context.Context, conversationID, filePath string) (geminiPart, error) {
	data, err := t.storageSvc.ReadFile(ctx, conversationID, filePath)
	if err != nil {
		return geminiPart{}, fmt.Errorf("read file by path: %w", err)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return geminiPart{}, fmt.Errorf("not an image: %s", mime)
	}
	return geminiPart{
		InlineData: &geminiInlineIn{
			MimeType: mime,
			Data:     base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

func normalizeCloudflareGatewayBase(endpoint string) string {
	const chatSuffix = "/compat/chat/completions"
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return endpoint
	}
	return strings.TrimSuffix(endpoint, chatSuffix)
}
