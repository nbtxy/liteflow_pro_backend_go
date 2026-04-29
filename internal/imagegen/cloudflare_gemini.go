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

type CloudflareGeminiProvider struct {
	httpClient *http.Client
	endpoint   string
	token      string
	model      string
}

func NewCloudflareGeminiProvider(cfg config.CloudflareConfig) *CloudflareGeminiProvider {
	model := strings.TrimSpace(cfg.ImageModel)
	if model == "" {
		model = "gemini-3.1-flash-image-preview"
	}
	return &CloudflareGeminiProvider{
		httpClient: &http.Client{},
		endpoint:   normalizeCloudflareGatewayBase(cfg.Endpoint),
		token:      strings.TrimSpace(cfg.Token),
		model:      model,
	}
}

func (p *CloudflareGeminiProvider) Name() string { return "cloudflare-gemini-image" }

func (p *CloudflareGeminiProvider) Generate(ctx context.Context, req *Request, textSink TextDeltaSink) (*Response, error) {
	if strings.TrimSpace(p.endpoint) == "" || strings.TrimSpace(p.token) == "" {
		return nil, fmt.Errorf("image generation not configured")
	}
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("image model is empty")
	}

	parts := []geminiPart{{Text: req.Prompt}}
	for _, ref := range req.ReferenceImages {
		if len(ref.Data) == 0 {
			continue
		}
		mime := strings.TrimSpace(ref.MimeType)
		if mime == "" {
			mime = "image/png"
		}
		parts = append(parts, geminiPart{
			InlineData: &geminiInlineIn{
				MimeType: mime,
				Data:     base64.StdEncoding.EncodeToString(ref.Data),
			},
		})
	}

	genConfig := map[string]any{
		"responseModalities": []string{"IMAGE", "TEXT"},
	}
	imageConfig := map[string]any{}
	if req.AspectRatio != "" {
		imageConfig["aspectRatio"] = req.AspectRatio
	}
	if strings.TrimSpace(req.Resolution) != "" {
		imageConfig["imageSize"] = strings.TrimSpace(req.Resolution)
	}
	// NOTE:
	// Current Google AI Studio streamGenerateContent schema behind this gateway
	// does not accept imageConfig.numberOfImages.
	// Keep provider-specific compatibility by only sending supported fields.
	if len(imageConfig) > 0 {
		genConfig["imageConfig"] = imageConfig
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
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	url := fmt.Sprintf("%s/google-ai-studio/v1beta/models/%s:generateContent", p.endpoint, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("cf-aig-authorization", "Bearer "+p.token)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("image gateway error: %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	rawResponseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	rawResponse := string(rawResponseBytes)

	var parsed geminiGenerateResponse
	if unmarshalErr := json.Unmarshal(rawResponseBytes, &parsed); unmarshalErr != nil {
		slog.Error("gemini response parse failed", "model", model, "err", unmarshalErr, "raw_response", truncateForLog(rawResponse, 8192))
		return nil, fmt.Errorf("parse response failed: %w", unmarshalErr)
	}

	var imageB64 strings.Builder
	mimeType := "image/png"
	usageAcc := &llm.LlmUsage{}
	if parsed.UsageMetadata != nil {
		usageAcc.InputTokens = parsed.UsageMetadata.PromptTokenCount
		usageAcc.OutputTokens = parsed.UsageMetadata.CandidatesTokenCount
	}
	for _, cand := range parsed.Candidates {
		for _, part := range cand.Content.Parts {
			if part.Text != "" && textSink != nil {
				textSink(part.Text)
			}
			if part.InlineData != nil && part.InlineData.Data != "" {
				imageB64.WriteString(part.InlineData.Data)
				if part.InlineData.MimeType != "" {
					mimeType = part.InlineData.MimeType
				}
			}
		}
	}

	if imageB64.Len() == 0 {
		slog.Error("gemini returned no image data", "model", model, "raw_response", rawResponse)
		return nil, fmt.Errorf("no image data returned")
	}

	imageBytes, err := base64.StdEncoding.DecodeString(imageB64.String())
	if err != nil {
		return nil, fmt.Errorf("decode image failed: %w", err)
	}

	return &Response{
		ImageBytes: imageBytes,
		MimeType:   mimeType,
		Usage:      usageAcc,
	}, nil
}

type geminiPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *geminiInlineIn `json:"inlineData,omitempty"`
}

type geminiInlineIn struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiGenerateResponse struct {
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

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func normalizeCloudflareGatewayBase(endpoint string) string {
	const chatSuffix = "/compat/chat/completions"
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return endpoint
	}
	return strings.TrimSuffix(endpoint, chatSuffix)
}
