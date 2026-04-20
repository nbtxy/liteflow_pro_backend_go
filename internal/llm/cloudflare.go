package llm

import (
	"strings"

	"github.com/liteflow/backend/internal/config"
)

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
		Endpoint:  normalizeCloudflareChatEndpoint(cfg.Endpoint),
		Model:     model,
		MaxTokens: 8000,
		Vision:    true,
		Headers:   headers,
	})
}

func normalizeCloudflareChatEndpoint(endpoint string) string {
	const chatSuffix = "/compat/chat/completions"
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return endpoint
	}
	if strings.HasSuffix(endpoint, chatSuffix) {
		return endpoint
	}
	return endpoint + chatSuffix
}
