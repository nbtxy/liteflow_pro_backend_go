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

	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/platform/events"
	"github.com/liteflow/backend/internal/platform/storage"
)

type ImageGenerateTool struct {
	storageSvc     storage.Service
	createArtifact CreateImageArtifactFunc
	httpClient     *http.Client
	cfEndpoint     string
	cfToken        string
	model          string
}

func NewImageGenerate(
	storageSvc storage.Service,
	createArtifact CreateImageArtifactFunc,
	cfg config.CloudflareConfig,
) *ImageGenerateTool {
	return &ImageGenerateTool{
		storageSvc:     storageSvc,
		createArtifact: createArtifact,
		httpClient:     &http.Client{},
		cfEndpoint:     strings.TrimRight(cfg.Endpoint, "/"),
		cfToken:        cfg.Token,
		model:          cfg.ImageModel,
	}
}

func (t *ImageGenerateTool) Name() string { return "generate_image" }

func (t *ImageGenerateTool) Description() string {
	return "通过 Cloudflare AI Gateway 调用 Google Nano Banana 2 生成图像，支持可选参考图和宽高比，结果以 IMAGE artifact 返回"
}

func (t *ImageGenerateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "图像描述",
			},
			"aspect_ratio": map[string]any{
				"type":        "string",
				"enum":        []string{"1:1", "16:9", "9:16", "4:3", "3:4"},
				"description": "可选宽高比，默认由模型决定",
			},
			"reference_image_urls": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "可选参考图 URL 列表，用于图生图或风格参考",
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

	parts := []geminiPart{{Text: prompt}}
	if refs, ok := input["reference_image_urls"].([]any); ok {
		for _, raw := range refs {
			url, _ := raw.(string)
			if url == "" {
				continue
			}
			refPart, err := fetchReferencePart(ctx, url)
			if err != nil {
				slog.Warn("reference image fetch failed", "url", url, "err", err)
				continue
			}
			parts = append(parts, refPart)
		}
	}

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
	reader := bufio.NewReader(resp.Body)
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
					slog.Warn("gemini chunk parse failed", "err", err, "payload", truncate(payload, 200))
				} else {
					for _, cand := range chunk.Candidates {
						for _, part := range cand.Content.Parts {
							if part.Text != "" {
								events.Emit(ctx, events.NewEvent("text_delta", map[string]any{
									"content": part.Text,
									"source":  "tool:generate_image",
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

	return &ToolResult{
		Content:  fmt.Sprintf("图像已生成: %s (%d bytes)", filename, len(imageBytes)),
		Metadata: metadata,
	}, nil
}

func fetchReferencePart(ctx context.Context, url string) (geminiPart, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return geminiPart{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LiteFlow/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return geminiPart{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return geminiPart{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return geminiPart{}, err
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = http.DetectContentType(data)
	}
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
