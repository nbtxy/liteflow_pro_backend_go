package tool

import (
	"context"
	"fmt"
)

type WebSearchTool struct {
	// TODO: inject SearchProviderRouter
}

func NewWebSearch() *WebSearchTool {
	return &WebSearchTool{}
}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string  { return "Search the web for information" }
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

func (t *WebSearchTool) Execute(_ context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	query, _ := input["query"].(string)
	if query == "" {
		return &ToolResult{Content: "query is required", IsError: true}, nil
	}

	// TODO: implement with actual search provider
	return &ToolResult{
		Content: fmt.Sprintf("Search results for: %s (not yet implemented)", query),
	}, nil
}
