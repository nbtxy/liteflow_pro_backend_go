package tool

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var delegateAgentNameCleaner = regexp.MustCompile(`[^a-z0-9_]+`)

type DelegateRunner func(ctx context.Context, subAgentID, subAgentName, task, extraContext string, tc *ToolContext) (string, error)

type DelegateAgent struct {
	subAgentID   string
	subAgentName string
	description  string
	runner       DelegateRunner
	toolName     string
}

func NewDelegateAgent(subAgentID, subAgentName, description string, runner DelegateRunner) *DelegateAgent {
	name := strings.TrimSpace(strings.ToLower(subAgentID))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = delegateAgentNameCleaner.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "sub_agent"
	}
	return &DelegateAgent{
		subAgentID:   subAgentID,
		subAgentName: strings.TrimSpace(subAgentName),
		description:  strings.TrimSpace(description),
		runner:       runner,
		toolName:     "delegate_to_" + name,
	}
}

func (t *DelegateAgent) Name() string {
	return t.toolName
}

func (t *DelegateAgent) Description() string {
	if t.description != "" {
		return t.description
	}
	return "将任务委派给专业子 Agent 处理"
}

func (t *DelegateAgent) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "要委派给该子 Agent 的具体任务描述",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "需要传递给子 Agent 的额外上下文信息（可选）",
			},
		},
		"required": []string{"task"},
	}
}

func (t *DelegateAgent) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	if t.runner == nil {
		return &ToolResult{Content: "子 Agent 执行器未配置", IsError: true}, nil
	}
	task, _ := input["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return &ToolResult{Content: "task 参数不能为空", IsError: true}, nil
	}
	extraContext, _ := input["context"].(string)
	_, err := t.runner(ctx, t.subAgentID, t.subAgentName, task, extraContext, tc)
	if err != nil {
		return &ToolResult{
			Content: fmt.Sprintf("子 Agent 执行失败: %v", err),
			IsError: true,
		}, nil
	}
	return &ToolResult{Content: "子 Agent 执行成功"}, nil
}
