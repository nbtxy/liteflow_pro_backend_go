package tool

import (
	"context"
	"fmt"
	"time"
)

type CurrentTimeTool struct{}

func NewCurrentTime() *CurrentTimeTool {
	return &CurrentTimeTool{}
}

func (t *CurrentTimeTool) Name() string        { return "current_time" }
func (t *CurrentTimeTool) Description() string  { return "Get the current date and time" }
func (t *CurrentTimeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"timezone": map[string]any{
				"type":        "string",
				"description": "Timezone name, e.g. 'Asia/Shanghai', 'UTC'. Defaults to Asia/Shanghai.",
			},
		},
	}
}

func (t *CurrentTimeTool) Execute(_ context.Context, input map[string]any, _ *ToolContext) (*ToolResult, error) {
	tz := "Asia/Shanghai"
	if v, ok := input["timezone"].(string); ok && v != "" {
		tz = v
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return &ToolResult{
			Content: fmt.Sprintf("invalid timezone: %s", tz),
			IsError: true,
		}, nil
	}

	now := time.Now().In(loc)
	return &ToolResult{
		Content: fmt.Sprintf("Current time (%s): %s\nDay of week: %s",
			tz, now.Format("2006-01-02 15:04:05"), now.Weekday().String()),
	}, nil
}
