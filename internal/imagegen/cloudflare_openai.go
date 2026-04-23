package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/llm"
)

type CloudflareOpenAIImageProvider struct {
	httpClient   *http.Client
	endpoint     string
	gatewayToken string
	openAIAPIKey string
	model        string
}

func NewCloudflareOpenAIImageProvider(cfg config.CloudflareConfig) *CloudflareOpenAIImageProvider {
	model := normalizeOpenAIImageModel(cfg.OpenAIImageModel)
	if model == "" {
		model = "gpt-image-2"
	}
	return &CloudflareOpenAIImageProvider{
		httpClient:   &http.Client{},
		endpoint:     normalizeCloudflareGatewayBase(cfg.Endpoint),
		gatewayToken: strings.TrimSpace(cfg.Token),
		openAIAPIKey: strings.TrimSpace(cfg.OpenAIAPIKey),
		model:        model,
	}
}

func (p *CloudflareOpenAIImageProvider) Name() string { return "cloudflare-openai-image" }

func (p *CloudflareOpenAIImageProvider) Generate(ctx context.Context, req *Request, textSink TextDeltaSink) (*Response, error) {
	if strings.TrimSpace(p.endpoint) == "" || strings.TrimSpace(p.gatewayToken) == "" || strings.TrimSpace(p.openAIAPIKey) == "" {
		return nil, fmt.Errorf("image generation not configured")
	}
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	model := normalizeOpenAIImageModel(req.Model)
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("image model is empty")
	}

	body := map[string]any{
		"prompt":  req.Prompt,
		"size":    resolveOpenAIImageSize(req.Resolution, req.AspectRatio),
		"n":       clampImageCount(req.ImageCount),
		"model":   model,
		"quality": "auto",
	}
	if len(req.ReferenceImages) > 0 {
		images := make([]string, 0, len(req.ReferenceImages))
		for _, ref := range req.ReferenceImages {
			if len(ref.Data) == 0 {
				continue
			}
			images = append(images, base64.StdEncoding.EncodeToString(ref.Data))
		}
		if len(images) == 1 {
			body["image"] = images[0]
		} else if len(images) > 1 {
			body["image"] = images
		}
		// edits with multiple reference images; force n=1 for OpenAI edits semantics
		body["n"] = 1
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	url := fmt.Sprintf("%s/openai/v1/images/generations", p.endpoint)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	openAIAuthHeader := strings.TrimSpace(p.openAIAPIKey)
	if !strings.HasPrefix(strings.ToLower(openAIAuthHeader), "bearer ") {
		openAIAuthHeader = "Bearer " + openAIAuthHeader
	}
	gatewayAuthHeader := strings.TrimSpace(p.gatewayToken)
	if !strings.HasPrefix(strings.ToLower(gatewayAuthHeader), "bearer ") {
		gatewayAuthHeader = "Bearer " + gatewayAuthHeader
	}
	httpReq.Header.Set("Authorization", openAIAuthHeader)
	httpReq.Header.Set("cf-aig-authorization", gatewayAuthHeader)

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
		return nil, fmt.Errorf("image gateway error: %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if len(respBody) == 0 {
		return nil, fmt.Errorf("image gateway error: empty response body")
	}

	b64, usage, parseErr := parseOpenAIImageResponse(respBody)
	if parseErr != nil {
		return nil, parseErr
	}
	if b64 == "" {
		return nil, fmt.Errorf("no image data returned")
	}

	imageBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode image failed: %w", err)
	}

	return &Response{
		ImageBytes: imageBytes,
		MimeType:   "image/png",
		Usage:      usage,
	}, nil
}

type openAIImageData struct {
	B64JSON string `json:"b64_json"`
}

type openAIImageUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type openAIImageEnvelope struct {
	Success *bool `json:"success,omitempty"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
	Result *struct {
		Data  []openAIImageData `json:"data"`
		Usage *openAIImageUsage `json:"usage,omitempty"`
	} `json:"result,omitempty"`
	Data  []openAIImageData `json:"data,omitempty"`
	Usage *openAIImageUsage `json:"usage,omitempty"`
}

func parseOpenAIImageResponse(body []byte) (string, *llm.LlmUsage, error) {
	var env openAIImageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", nil, fmt.Errorf("parse response failed: %w", err)
	}
	if env.Success != nil && !*env.Success {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			if e.Message != "" {
				msgs = append(msgs, e.Message)
			}
		}
		if len(msgs) == 0 {
			return "", nil, fmt.Errorf("image gateway request unsuccessful")
		}
		return "", nil, fmt.Errorf("image gateway error: %s", strings.Join(msgs, "; "))
	}

	var data []openAIImageData
	var usageSrc *openAIImageUsage
	if env.Result != nil {
		data = env.Result.Data
		usageSrc = env.Result.Usage
	}
	if len(data) == 0 {
		data = env.Data
	}
	if usageSrc == nil {
		usageSrc = env.Usage
	}
	if len(data) == 0 {
		return "", nil, nil
	}

	var usage *llm.LlmUsage
	if usageSrc != nil && (usageSrc.InputTokens > 0 || usageSrc.OutputTokens > 0) {
		usage = &llm.LlmUsage{
			InputTokens:  usageSrc.InputTokens,
			OutputTokens: usageSrc.OutputTokens,
		}
	}

	return data[0].B64JSON, usage, nil
}

func resolveOpenAIImageSize(resolution, aspectRatio string) string {
	if s := strings.TrimSpace(resolution); s != "" {
		return s
	}
	switch strings.TrimSpace(aspectRatio) {
	case "1:1":
		return "1024x1024"
	case "16:9":
		return "1792x1024"
	case "9:16":
		return "1024x1792"
	case "4:3":
		return "1536x1024"
	case "3:4":
		return "1024x1536"
	default:
		return "auto"
	}
}

func clampImageCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > 10 {
		return 10
	}
	return n
}

func normalizeOpenAIImageModel(model string) string {
	m := strings.TrimSpace(model)
	switch strings.ToLower(m) {
	case "", "openai/gpt-image-1.5", "gpt-image-1.5":
		return "gpt-image-1"
	case "openai/gpt-image-1":
		return "gpt-image-1"
	default:
		return m
	}
}
