package llm

import "github.com/liteflow/backend/internal/config"

func NewOpenRouterProvider(cfg config.ProviderConfig) *BaseOpenAIProvider {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://openrouter.ai/api/v1/chat/completions"
	}
	model := cfg.Model
	if model == "" {
		model = "anthropic/claude-3.5-sonnet"
	}

	return NewBaseOpenAIProvider(BaseOpenAIConfig{
		Name:      "openrouter",
		APIKey:    cfg.APIKey,
		Endpoint:  endpoint,
		Model:     model,
		MaxTokens: 128000,
		Vision:    true,
	})
}
