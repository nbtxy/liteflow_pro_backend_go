package llm

import "encoding/json"

// LlmMessage represents a single message in the LLM conversation.
type LlmMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	Images     []ImageContent  `json:"images,omitempty"` // for vision models
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ImageContent struct {
	Type     string    `json:"type"` // image_url
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

// ToolDefinition is the JSON Schema definition of a tool for the LLM.
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// LlmRequest is the request to send to an LLM provider.
type LlmRequest struct {
	Messages    []LlmMessage     `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Stream      bool             `json:"stream"`
	Model       string           `json:"model,omitempty"`
}

// LlmResponse is the complete response from a non-streaming LLM call.
type LlmResponse struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     *LlmUsage `json:"usage,omitempty"`
}

// LlmChunk is a single chunk from a streaming LLM response.
type LlmChunk struct {
	Content string `json:"content,omitempty"`

	// Tool call delta fields
	ToolCallIndex          int    `json:"-"`
	ToolCallID             string `json:"-"`
	ToolCallFunctionName   string `json:"-"`
	ToolCallFunctionArgs   string `json:"-"`

	Usage    *LlmUsage `json:"usage,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`

	// Error indicates a stream error
	Err error `json:"-"`
}

func (c LlmChunk) HasToolCallDelta() bool {
	return c.ToolCallID != "" || c.ToolCallFunctionName != "" || c.ToolCallFunctionArgs != ""
}

// LlmUsage tracks token consumption.
type LlmUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (u *LlmUsage) Add(other *LlmUsage) {
	if other == nil {
		return
	}
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
}

func (u *LlmUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// ParseToolCallArguments parses the arguments JSON string into a map.
func ParseToolCallArguments(args string) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal([]byte(args), &result); err != nil {
		return nil, err
	}
	return result, nil
}
