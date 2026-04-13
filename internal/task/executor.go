package task

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
	"github.com/liteflow/backend/internal/llm"
)

type OutputSender interface {
	SendToConversation(ctx context.Context, userID, convID uuid.UUID, taskName, content string) error
	SendToFeishu(ctx context.Context, userID uuid.UUID, taskName, content string) error
}

type Executor struct {
	pool     *pgxpool.Pool
	provider *llm.ProviderRouter
	sender   OutputSender
}

func NewExecutor(pool *pgxpool.Pool) *Executor {
	return &Executor{pool: pool}
}

func (e *Executor) SetProvider(p *llm.ProviderRouter) { e.provider = p }
func (e *Executor) SetOutputSender(s OutputSender)    { e.sender = s }

func (e *Executor) Run(parentCtx context.Context, task *domain.ScheduledTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	slog.Info("executing task", "taskId", task.ID, "name", task.Name)

	startTime := time.Now()
	exec := &domain.TaskExecution{
		ID:        uuid.New(),
		TaskID:    task.ID,
		Status:    "running",
		CreatedAt: startTime,
	}
	e.saveExecution(ctx, exec)

	provider := e.provider.Default()
	if provider == nil {
		errMsg := "no LLM provider available"
		exec.Status = "failed"
		exec.ErrorMessage = &errMsg
		e.finishExecution(ctx, exec, task, startTime)
		return
	}

	temp := 0.7
	req := &llm.LlmRequest{
		Messages: []llm.LlmMessage{
			{Role: "system", Content: fmt.Sprintf("你是定时任务执行引擎。请执行以下任务并给出结果摘要。任务名称：%s", task.Name)},
			{Role: "user", Content: task.Prompt},
		},
		MaxTokens:   4096,
		Temperature: &temp,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		errMsg := err.Error()
		exec.Status = "failed"
		exec.ErrorMessage = &errMsg
		e.finishExecution(ctx, exec, task, startTime)
		return
	}

	exec.Status = "success"
	exec.ResultSummary = &resp.Content
	if resp.Usage != nil {
		in := int32(resp.Usage.InputTokens)
		out := int32(resp.Usage.OutputTokens)
		exec.InputTokens = &in
		exec.OutputTokens = &out
	}

	e.finishExecution(ctx, exec, task, startTime)

	e.deliverOutput(ctx, task, resp.Content)
}

func (e *Executor) finishExecution(ctx context.Context, exec *domain.TaskExecution, task *domain.ScheduledTask, startTime time.Time) {
	dur := int32(time.Since(startTime).Milliseconds())
	exec.DurationMs = &dur
	e.saveExecution(ctx, exec)

	now := time.Now()
	e.pool.Exec(ctx,
		`UPDATE scheduled_tasks SET last_run_at = $1, last_run_status = $2, updated_at = $3 WHERE id = $4`,
		now, exec.Status, now, task.ID)

	if task.TaskType == "once" {
		e.pool.Exec(ctx,
			`UPDATE scheduled_tasks SET status = 'completed', updated_at = $1 WHERE id = $2`,
			now, task.ID)
	}

	slog.Info("task execution finished", "taskId", task.ID, "status", exec.Status, "durationMs", dur)
}

func (e *Executor) deliverOutput(ctx context.Context, task *domain.ScheduledTask, content string) {
	if e.sender == nil {
		return
	}

	targets := parseOutputTargets(task.OutputConfig)
	for _, t := range targets {
		switch t {
		case "conversation":
			convID := task.ConversationID
			if convID == nil {
				continue
			}
			if err := e.sender.SendToConversation(ctx, task.UserID, *convID, task.Name, content); err != nil {
				slog.Error("failed to send task result to conversation", "taskId", task.ID, "err", err)
			}
		case "feishu":
			if err := e.sender.SendToFeishu(ctx, task.UserID, task.Name, content); err != nil {
				slog.Error("failed to send task result to feishu", "taskId", task.ID, "err", err)
			}
		}
	}
}

func parseOutputTargets(raw json.RawMessage) []string {
	if raw == nil {
		return []string{"conversation"}
	}
	var cfg struct {
		Targets []struct {
			Type string `json:"type"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Targets) == 0 {
		return []string{"conversation"}
	}
	out := make([]string, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		out = append(out, t.Type)
	}
	return out
}

func (e *Executor) saveExecution(ctx context.Context, exec *domain.TaskExecution) {
	toolsJSON, _ := json.Marshal([]string{})
	targetsJSON, _ := json.Marshal(map[string]any{})

	_, err := e.pool.Exec(ctx,
		`INSERT INTO task_executions (id, task_id, status, result_summary, output_targets,
		 input_tokens, output_tokens, tools_used, duration_ms, error_message, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (id) DO UPDATE SET status = $3, result_summary = $4,
		 input_tokens = $6, output_tokens = $7, tools_used = $8, duration_ms = $9, error_message = $10`,
		exec.ID, exec.TaskID, exec.Status, exec.ResultSummary, targetsJSON,
		exec.InputTokens, exec.OutputTokens, toolsJSON, exec.DurationMs, exec.ErrorMessage, exec.CreatedAt)
	if err != nil {
		slog.Error("failed to save task execution", "err", err)
	}
}

func (e *Executor) CountExecutions(ctx context.Context, taskID uuid.UUID) (int, error) {
	var count int
	err := e.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM task_executions WHERE task_id = $1`, taskID).Scan(&count)
	return count, err
}

func (e *Executor) GetExecutionsPaged(ctx context.Context, taskID uuid.UUID, page, size int) ([]domain.TaskExecution, error) {
	offset := page * size
	rows, err := e.pool.Query(ctx,
		`SELECT id, task_id, status, result_summary, output_targets,
		        input_tokens, output_tokens, tools_used, duration_ms, error_message, created_at
		 FROM task_executions WHERE task_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		taskID, size, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []domain.TaskExecution
	for rows.Next() {
		var ex domain.TaskExecution
		if err := rows.Scan(&ex.ID, &ex.TaskID, &ex.Status, &ex.ResultSummary, &ex.OutputTargets,
			&ex.InputTokens, &ex.OutputTokens, &ex.ToolsUsed, &ex.DurationMs, &ex.ErrorMessage,
			&ex.CreatedAt); err != nil {
			return nil, err
		}
		execs = append(execs, ex)
	}
	return execs, nil
}
