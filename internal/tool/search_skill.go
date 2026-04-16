package tool

import (
	"context"
	"strings"
)

type SkillSearchWithWhitelistFunc func(query string, whitelist []string) string
type SkillListFunc func() []string

type SearchSkillTool struct {
	searchFn SkillSearchWithWhitelistFunc
	listFn   SkillListFunc
}

func NewSearchSkill(searchFn SkillSearchWithWhitelistFunc, listFn SkillListFunc) *SearchSkillTool {
	return &SearchSkillTool{searchFn: searchFn, listFn: listFn}
}

func (t *SearchSkillTool) Name() string { return "search_skill" }
func (t *SearchSkillTool) Description() string {
	base := "Search for available skills to handle specific tasks."
	if t.listFn == nil {
		return base
	}

	names := t.listFn()
	if len(names) == 0 {
		return base
	}
	return base + " Available skills: " + strings.Join(names, ", ") + "."
}

func (t *SearchSkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query describing the task",
			},
			"whitelist": map[string]any{
				"type":        "array",
				"description": "Optional skill name whitelist; when present only listed skills are visible.",
				"items": map[string]any{
					"type": "string",
				},
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

	var whitelist []string
	if arr, ok := input["whitelist"].([]string); ok {
		whitelist = append(whitelist, arr...)
	}
	if arr, ok := input["whitelist"].([]any); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok && s != "" {
				whitelist = append(whitelist, s)
			}
		}
	}

	result := t.searchFn(query, whitelist)
	return &ToolResult{Content: result}, nil
}
