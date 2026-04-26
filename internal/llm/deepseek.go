package llm

import "github.com/liteflow/backend/internal/config"

func NewDeepSeekProvider(cfg config.ProviderConfig) *BaseOpenAIProvider {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.deepseek.com/chat/completions"
	}
	model := cfg.Model
	if model == "" {
		model = "deepseek-chat"
	}

	return NewBaseOpenAIProvider(BaseOpenAIConfig{
		Name:      "deepseek",
		APIKey:    cfg.APIKey,
		Endpoint:  endpoint,
		Model:     model,
		MaxTokens: 1000000,
		Vision:    false,
	})
}
