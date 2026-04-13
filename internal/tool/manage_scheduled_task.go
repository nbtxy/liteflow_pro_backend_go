package tool

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

type TaskManageFunc func(ctx context.Context, userID fmt.Stringer, action string, input map[string]any) (string, error)

type ManageScheduledTaskTool struct {
	manageFn TaskManageFunc
}

func NewManageScheduledTask(manageFn TaskManageFunc) *ManageScheduledTaskTool {
	return &ManageScheduledTaskTool{manageFn: manageFn}
}

func (t *ManageScheduledTaskTool) Name() string { return "manage_scheduled_task" }
func (t *ManageScheduledTaskTool) Description() string {
	return "创建、修改、暂停、恢复或删除定时任务。" +
		"支持两种类型：周期性任务（recurring，需要 cron_expression）和一次性任务（once，需要 run_at）。\n\n" +
		"支持的操作：\n" +
		"- create: 创建新任务\n" +
		"- list: 列出当前所有任务\n" +
		"- update: 修改任务（prompt、时间、输出渠道）\n" +
		"- pause: 暂停任务\n" +
		"- resume: 恢复任务\n" +
		"- delete: 删除任务\n" +
		"- run_now: 立即执行一次（测试用）"
}

func (t *ManageScheduledTaskTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "list", "update", "pause", "resume", "delete", "run_now"},
				"description": "操作类型",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "任务 ID（update/pause/resume/delete/run_now 时必填）",
			},
			"task_type": map[string]any{
				"type":        "string",
				"enum":        []string{"recurring", "once"},
				"description": "任务类型。recurring=周期性任务（需要 cron_expression），once=一次性任务（需要 run_at）。默认 recurring",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "任务名称（create 时必填）",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "任务执行时的 prompt（create 时必填）",
			},
			"cron_expression": map[string]any{
				"type": "string",
				"description": "Cron 表达式，周期性任务必填，Spring 6位格式（秒 分 时 日 月 周）。" +
					"示例：'0 0 9 * * ?' 每天9点，'0 0 9 ? * MON,WED,FRI' 每周一三五9点，" +
					"'0 0 * * * ?' 每小时，'0 0 8 ? * MON-FRI' 工作日8点",
			},
			"run_at": map[string]any{
				"type":        "string",
				"description": "一次性任务的执行时间，ISO 8601 格式（如 '2026-04-01T15:00:00+08:00'）。一次性任务必填",
			},
			"timezone": map[string]any{
				"type":        "string",
				"description": "时区，默认 Asia/Shanghai",
			},
			"output_targets": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": []string{"conversation", "feishu"}},
				"description": "结果推送渠道，默认只推送到 LiteFlow 对话。只有用户明确要求发飞书时才加 feishu",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManageScheduledTaskTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	action, _ := input["action"].(string)
	if action == "" {
		return &ToolResult{Content: "action 参数不能为空", IsError: true}, nil
	}

	if t.manageFn == nil {
		return &ToolResult{Content: "task service not available", IsError: true}, nil
	}

	if tc.MessageID != (uuid.UUID{}) {
		input["_source_message_id"] = tc.MessageID.String()
	}
	if tc.ConversationID != (uuid.UUID{}) {
		input["_conversation_id"] = tc.ConversationID.String()
	}

	result, err := t.manageFn(ctx, tc.UserID, action, input)
	if err != nil {
		promptLen := 0
		if p, ok := input["prompt"].(string); ok {
			promptLen = len([]rune(p))
		}
		slog.Error("manage_scheduled_task execute failed",
			"action", action,
			"userId", tc.UserID.String(),
			"conversationId", input["_conversation_id"],
			"sourceMessageId", input["_source_message_id"],
			"taskId", input["task_id"],
			"taskType", input["task_type"],
			"cronExpression", input["cron_expression"],
			"promptLen", promptLen,
			"err", err,
		)
		return &ToolResult{Content: err.Error(), IsError: true}, nil
	}

	return &ToolResult{Content: result}, nil
}
