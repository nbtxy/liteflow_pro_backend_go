package imagegen

import (
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

	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/llm"
)

type OpenRouterImageProvider struct {
	httpClient *http.Client
	endpoint   string
	apiKey     string
	aigToken   string
	model      string
	referer    string
	title      string
}

func NewOpenRouterImageProvider(cfg config.ProviderConfig, defaultModel, cfGatewayBase, aigToken string) *OpenRouterImageProvider {
	endpoint := resolveOpenRouterEndpoint(cfg.Endpoint, cfGatewayBase)
	model := strings.TrimSpace(defaultModel)
	if model == "" {
		model = strings.TrimSpace(cfg.Model)
	}
	if model == "" {
		model = "openai/gpt-5.4-image-2"
	}
	slog.Info("openrouter image provider configured",
		"endpoint", endpoint,
		"model", model,
		"viaCfGateway", strings.Contains(endpoint, "gateway.ai.cloudflare.com"),
		"hasAigToken", strings.TrimSpace(aigToken) != "",
	)
	return &OpenRouterImageProvider{
		httpClient: &http.Client{},
		endpoint:   endpoint,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		aigToken:   strings.TrimSpace(aigToken),
		model:      model,
		referer:    "https://liteflow.app",
		title:      "Liteflow",
	}
}

func (p *OpenRouterImageProvider) Name() string { return "openrouter-image" }

func (p *OpenRouterImageProvider) Generate(ctx context.Context, req *Request, textSink TextDeltaSink) (*Response, error) {
	if p.endpoint == "" || p.apiKey == "" {
		return nil, fmt.Errorf("image generation not configured")
	}
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.model
	}

	content := []map[string]any{
		{"type": "text", "text": req.Prompt},
	}
	for _, ref := range req.ReferenceImages {
		var url string
		if u := strings.TrimSpace(ref.URL); u != "" {
			url = u
		} else if len(ref.Data) > 0 {
			mime := strings.TrimSpace(ref.MimeType)
			if mime == "" {
				mime = "image/png"
			}
			url = fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(ref.Data))
		} else {
			continue
		}
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": url},
		})
	}

	body := map[string]any{
		"model":      model,
		"modalities": []string{"image", "text"},
		"messages": []map[string]any{
			{"role": "user", "content": content},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	auth := p.apiKey
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		auth = "Bearer " + auth
	}
	httpReq.Header.Set("Authorization", auth)
	hasAig := false
	if p.aigToken != "" {
		aig := p.aigToken
		if !strings.HasPrefix(strings.ToLower(aig), "bearer ") {
			aig = "Bearer " + aig
		}
		httpReq.Header.Set("cf-aig-authorization", aig)
		hasAig = true
	}
	slog.Info("openrouter image request",
		"url", p.endpoint,
		"model", model,
		"viaCfGateway", strings.Contains(p.endpoint, "gateway.ai.cloudflare.com"),
		"cfAigHeader", hasAig,
	)
	if p.referer != "" {
		httpReq.Header.Set("HTTP-Referer", p.referer)
	}
	if p.title != "" {
		httpReq.Header.Set("X-Title", p.title)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openrouter image error: %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	imageBytes, mime, text, usage, err := parseOpenRouterImageResponse(respBody)
	if err != nil {
		return nil, err
	}
	if len(imageBytes) == 0 {
		return nil, fmt.Errorf("no image data returned")
	}
	if textSink != nil && text != "" {
		textSink(text)
	}

	return &Response{
		ImageBytes: imageBytes,
		MimeType:   mime,
		Usage:      usage,
	}, nil
}

type openRouterImageResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Images  []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func parseOpenRouterImageResponse(body []byte) ([]byte, string, string, *llm.LlmUsage, error) {
	var env openRouterImageResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, "", "", nil, fmt.Errorf("parse response failed: %w", err)
	}
	if env.Error != nil && env.Error.Message != "" {
		return nil, "", "", nil, fmt.Errorf("openrouter image error: %s", env.Error.Message)
	}
	if len(env.Choices) == 0 {
		return nil, "", "", nil, nil
	}

	msg := env.Choices[0].Message
	for _, img := range msg.Images {
		data, mime, decodeErr := decodeDataURL(img.ImageURL.URL)
		if decodeErr != nil {
			return nil, "", "", nil, decodeErr
		}
		if len(data) == 0 {
			continue
		}
		var usage *llm.LlmUsage
		if env.Usage != nil && (env.Usage.PromptTokens > 0 || env.Usage.CompletionTokens > 0) {
			usage = &llm.LlmUsage{
				InputTokens:  env.Usage.PromptTokens,
				OutputTokens: env.Usage.CompletionTokens,
			}
		}
		return data, mime, msg.Content, usage, nil
	}
	return nil, "", msg.Content, nil, nil
}

func decodeDataURL(dataURL string) ([]byte, string, error) {
	const prefix = "data:"
	if !strings.HasPrefix(dataURL, prefix) {
		return nil, "", fmt.Errorf("unexpected image url format")
	}
	rest := dataURL[len(prefix):]
	commaIdx := strings.IndexByte(rest, ',')
	if commaIdx < 0 {
		return nil, "", fmt.Errorf("invalid data url")
	}
	meta := rest[:commaIdx]
	payload := rest[commaIdx+1:]
	mime := "image/png"
	isBase64 := false
	for _, part := range strings.Split(meta, ";") {
		part = strings.TrimSpace(part)
		if part == "base64" {
			isBase64 = true
			continue
		}
		if part != "" && !strings.Contains(part, "=") {
			mime = part
		}
	}
	if !isBase64 {
		return nil, "", fmt.Errorf("only base64 data urls are supported")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", fmt.Errorf("decode image failed: %w", err)
	}
	return data, mime, nil
}

func normalizeOpenRouterChatEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return "https://openrouter.ai/api/v1/chat/completions"
	}
	if strings.HasSuffix(endpoint, "/chat/completions") {
		return endpoint
	}
	return endpoint + "/chat/completions"
}

// resolveOpenRouterEndpoint picks the chat completions URL with priority:
//  1. Explicit OPENROUTER_ENDPOINT
//  2. Cloudflare AI Gateway base + /openrouter/v1/chat/completions
//  3. Direct openrouter.ai default
func resolveOpenRouterEndpoint(openRouterEndpoint, cfGatewayBase string) string {
	if strings.TrimSpace(openRouterEndpoint) != "" {
		return normalizeOpenRouterChatEndpoint(openRouterEndpoint)
	}
	base := normalizeCloudflareGatewayBase(cfGatewayBase)
	if base != "" {
		return base + "/openrouter/v1/chat/completions"
	}
	return normalizeOpenRouterChatEndpoint("")
}
