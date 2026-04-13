package tool

import (
	"context"
)

type SkillSearchFunc func(query string) string

type SearchSkillTool struct {
	searchFn SkillSearchFunc
}

func NewSearchSkill(searchFn SkillSearchFunc) *SearchSkillTool {
	return &SearchSkillTool{searchFn: searchFn}
}

func (t *SearchSkillTool) Name() string { return "search_skill" }
func (t *SearchSkillTool) Description() string {
	return "Search for available skills to handle specific tasks like PDF, XLSX, DOCX processing."
}

func (t *SearchSkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query describing the task",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SearchSkillTool) Execute(_ context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	query, _ := input["query"].(string)
	if query == "" {
		return &ToolResult{Content: "query is required", IsError: true}, nil
	}

	if t.searchFn == nil {
		return &ToolResult{Content: "skill registry not available", IsError: true}, nil
	}

	result := t.searchFn(query)
	return &ToolResult{Content: result}, nil
}
