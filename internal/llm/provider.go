package llm

import "context"

// Provider is the interface for LLM providers.
type Provider interface {
	Name() string
	StreamChat(ctx context.Context, req *LlmRequest) (<-chan LlmChunk, error)
	Chat(ctx context.Context, req *LlmRequest) (*LlmResponse, error)
	MaxContextTokens() int
	SupportsVision() bool
}
