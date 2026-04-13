package llm

import "github.com/liteflow/backend/internal/config"

func NewQwenProvider(cfg config.ProviderConfig) *BaseOpenAIProvider {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
	}
	model := cfg.Model
	if model == "" {
		model = "qwen-max"
	}

	return NewBaseOpenAIProvider(BaseOpenAIConfig{
		Name:      "qwen",
		APIKey:    cfg.APIKey,
		Endpoint:  endpoint,
		Model:     model,
		MaxTokens: 32000,
		Vision:    true,
	})
}
