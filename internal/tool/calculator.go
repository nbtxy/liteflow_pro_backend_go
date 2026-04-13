package tool

import (
	"context"
	"fmt"
)

type CalculatorTool struct{}

func NewCalculator() *CalculatorTool {
	return &CalculatorTool{}
}

func (t *CalculatorTool) Name() string        { return "calculator" }
func (t *CalculatorTool) Description() string  { return "Evaluate a mathematical expression" }
func (t *CalculatorTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{
				"type":        "string",
				"description": "Mathematical expression to evaluate",
			},
		},
		"required": []string{"expression"},
	}
}

func (t *CalculatorTool) Execute(_ context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	expr, _ := input["expression"].(string)
	if expr == "" {
		return &ToolResult{Content: "expression is required", IsError: true}, nil
	}

	// Simple expression evaluation placeholder
	// TODO: implement proper expression parser (or use a Go library)
	return &ToolResult{
		Content: fmt.Sprintf("Expression: %s (evaluation not yet implemented)", expr),
	}, nil
}
