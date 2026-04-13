package usage

var providerCostPer1K = map[string]struct {
	input  float64
	output float64
}{
	"deepseek":               {0.001, 0.002},
	"qwen":                   {0.002, 0.006},
	"openrouter":             {0.003, 0.015},
	"cloudflare-ai-gateway":  {0.0, 0.0},
}

func EstimateCost(provider string, inputTokens, outputTokens int) float64 {
	costs, ok := providerCostPer1K[provider]
	if !ok {
		return 0
	}
	return float64(inputTokens)/1000*costs.input + float64(outputTokens)/1000*costs.output
}
