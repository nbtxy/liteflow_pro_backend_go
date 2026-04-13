package llm

import "github.com/liteflow/backend/internal/config"

func NewCloudflareProvider(cfg config.CloudflareConfig) *BaseOpenAIProvider {
	model := cfg.Model
	if model == "" {
		model = "@cf/meta/llama-3.1-8b-instruct"
	}

	headers := map[string]string{}
	if cfg.Token != "" {
		headers["cf-aig-authorization"] = "Bearer " + cfg.Token
	}

	return NewBaseOpenAIProvider(BaseOpenAIConfig{
		Name:      "cloudflare-ai-gateway",
		Endpoint:  cfg.Endpoint,
		Model:     model,
		MaxTokens: 8000,
		Vision:    false,
		Headers:   headers,
	})
}
