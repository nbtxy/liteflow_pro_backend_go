package llm

import (
	"fmt"
	"strings"

	"github.com/liteflow/backend/internal/config"
)

func NewCloudflareProvider(cfg config.CloudflareConfig) *BaseOpenAIProvider {
	model := cfg.Model
	if model == "" {
		model = "anthropic/claude-opus-4-7"
	}

	headers := map[string]string{}
	if cfg.Token != "" {
		headers["cf-aig-authorization"] = "Bearer " + cfg.Token
	}
	if cfg.CacheTTL > 0 {
		headers["cf-aig-cache-ttl"] = fmt.Sprintf("%d", cfg.CacheTTL)
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
