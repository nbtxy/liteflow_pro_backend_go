package tool

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liteflow/backend/internal/platform/search"
)

type WebSearchTool struct {
	router *search.ProviderRouter
}

func NewWebSearch(router *search.ProviderRouter) *WebSearchTool {
	return &WebSearchTool{router: router}
}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string { return "Search the web for information" }
func (t *WebSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of results (default 5)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	query, _ := input["query"].(string)
	if query == "" {
		return &ToolResult{Content: "query is required", IsError: true}, nil
	}

	if t.router == nil {
		return &ToolResult{Content: "search provider is not configured", IsError: true}, nil
	}
	provider := t.router.Default()
	if provider == nil {
		return &ToolResult{Content: "no search provider available", IsError: true}, nil
	}

	count, err := parseCount(input["count"])
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("invalid count: %v", err), IsError: true}, nil
	}

	results, err := provider.Search(ctx, query, count)
	if err != nil {
		return &ToolResult{
			Content: fmt.Sprintf("search failed via %s: %v", provider.Name(), err),
			IsError: true,
		}, nil
	}

	if len(results) == 0 {
		return &ToolResult{
			Content: fmt.Sprintf("No search results found for: %s", query),
			Metadata: map[string]any{
				"provider": provider.Name(),
				"query":    query,
				"count":    0,
			},
		}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Search results for: %s\nProvider: %s\n\n", query, provider.Name())
	for i, item := range results {
		fmt.Fprintf(&b, "%d. %s\n", i+1, fallbackTitle(item.Title, item.URL))
		if item.URL != "" {
			fmt.Fprintf(&b, "   %s\n", item.URL)
		}
		if item.Content != "" {
			fmt.Fprintf(&b, "   %s\n", truncate(item.Content, 300))
		}
		b.WriteString("\n")
	}

	return &ToolResult{
		Content: strings.TrimSpace(b.String()),
		Metadata: map[string]any{
			"provider": provider.Name(),
			"query":    query,
			"count":    len(results),
			"results":  results,
		},
	}, nil
}

func parseCount(raw any) (int, error) {
	switch v := raw.(type) {
	case nil:
		return 0, nil
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, err
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", raw)
	}
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

func fallbackTitle(title, url string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	if strings.TrimSpace(url) != "" {
		return url
	}
	return "(untitled)"
}
