package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// BaseOpenAIProvider implements common OpenAI-compatible streaming logic.
type BaseOpenAIProvider struct {
	name       string
	apiKey     string
	endpoint   string
	model      string
	maxTokens  int
	vision     bool
	httpClient *http.Client
	headers    map[string]string // extra headers
}

type BaseOpenAIConfig struct {
	Name      string
	APIKey    string
	Endpoint  string
	Model     string
	MaxTokens int
	Vision    bool
	Headers   map[string]string
}

func NewBaseOpenAIProvider(cfg BaseOpenAIConfig) *BaseOpenAIProvider {
	return &BaseOpenAIProvider{
		name:       cfg.Name,
		apiKey:     cfg.APIKey,
		endpoint:   cfg.Endpoint,
		model:      cfg.Model,
		maxTokens:  cfg.MaxTokens,
		vision:     cfg.Vision,
		httpClient: &http.Client{},
		headers:    cfg.Headers,
	}
}

func (p *BaseOpenAIProvider) Name() string          { return p.name }
func (p *BaseOpenAIProvider) MaxContextTokens() int { return p.maxTokens }
func (p *BaseOpenAIProvider) SupportsVision() bool  { return p.vision }
func (p *BaseOpenAIProvider) Endpoint() string      { return p.endpoint }

func (p *BaseOpenAIProvider) StreamChat(ctx context.Context, req *LlmRequest) (<-chan LlmChunk, error) {
	body := p.buildRequestBody(req, true)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if provErr := ParseProviderError(resp.StatusCode, body); provErr != nil {
			return nil, provErr
		}
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(body))
	}

	ch := make(chan LlmChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// OpenAI-compatible SSE lines may exceed Scanner's default 64KB token limit,
		// especially when tool_call.arguments contains large payloads (e.g. create_file content).
		// Increase max token size to avoid silent truncation.
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()

			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				slog.Debug("stream done")
				break
			}

			chunk, err := p.extractChunk(data)
			if err != nil {
				// Provider error frames (200 + data: {"error":...}) must surface
				// to the caller; transient JSON parse failures get logged and
				// skipped so a single malformed frame doesn't abort the stream.
				var provErr *ErrProviderStreamError
				var ctxErr *ErrContextExceeded
				if errors.As(err, &ctxErr) || errors.As(err, &provErr) {
					select {
					case ch <- LlmChunk{Err: err}:
					case <-ctx.Done():
					}
					return
				}
				slog.Warn("failed to parse SSE chunk", "err", err, "data", data)
				continue
			}
			if chunk != nil {
				select {
				case ch <- *chunk:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case ch <- LlmChunk{Err: fmt.Errorf("read stream chunk: %w", err)}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

func (p *BaseOpenAIProvider) Chat(ctx context.Context, req *LlmRequest) (*LlmResponse, error) {
	body := p.buildRequestBody(req, false)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if provErr := ParseProviderError(resp.StatusCode, body); provErr != nil {
			return nil, provErr
		}
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(body))
	}

	var result openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.extractResponse(&result), nil
}

func (p *BaseOpenAIProvider) buildRequestBody(req *LlmRequest, stream bool) map[string]any {
	model := req.Model
	if model == "" {
		model = p.model
	}

	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		// Skip empty assistant stubs (no content, no tool_calls) to avoid
		// sending invalid/no-op turns like {"role":"assistant"} to providers.
		if msg.Role == "assistant" && strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
			continue
		}

		m := map[string]any{
			"role": msg.Role,
		}

		if len(msg.Images) > 0 && msg.Role == "user" {
			contentParts := make([]map[string]any, 0, len(msg.Images)+1)
			if msg.Content != "" {
				contentParts = append(contentParts, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, img := range msg.Images {
				contentParts = append(contentParts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]string{"url": img.ImageURL.URL},
				})
			}
			m["content"] = contentParts
		} else if msg.Content != "" {
			m["content"] = msg.Content
		}

		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}

		messages = append(messages, m)
	}

	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   stream,
	}

	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	return body
}

func (p *BaseOpenAIProvider) extractChunk(data string) (*LlmChunk, error) {
	if provErr := ParseProviderError(200, []byte(data)); provErr != nil {
		return nil, provErr
	}
	var raw openAIStreamChunk
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}

	chunk := &LlmChunk{}

	if len(raw.Choices) > 0 {
		delta := raw.Choices[0].Delta
		chunk.Content = delta.Content
		chunk.FinishReason = raw.Choices[0].FinishReason

		if len(delta.ToolCalls) > 0 {
			tc := delta.ToolCalls[0]
			chunk.ToolCallIndex = tc.Index
			chunk.ToolCallID = tc.ID
			chunk.ToolCallFunctionName = tc.Function.Name
			chunk.ToolCallFunctionArgs = tc.Function.Arguments
		}
	}

	if raw.Usage != nil {
		chunk.Usage = &LlmUsage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
		}
	}

	return chunk, nil
}

func (p *BaseOpenAIProvider) extractResponse(raw *openAIResponse) *LlmResponse {
	resp := &LlmResponse{}

	if len(raw.Choices) > 0 {
		msg := raw.Choices[0].Message
		resp.Content = msg.Content

		for _, tc := range msg.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: ToolCallFunc{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	if raw.Usage != nil {
		resp.Usage = &LlmUsage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
		}
	}

	return resp
}

// OpenAI API response structures

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}
