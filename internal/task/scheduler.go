package task

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/config"
	"github.com/liteflow/backend/internal/domain"
)

type Scheduler struct {
	pool     *pgxpool.Pool
	cfg      config.TasksConfig
	executor *Executor
}

func NewScheduler(pool *pgxpool.Pool, cfg config.TasksConfig, executor *Executor) *Scheduler {
	return &Scheduler{
		pool:     pool,
		cfg:      cfg,
		executor: executor,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		slog.Info("task scheduler disabled")
		return
	}

	slog.Info("task scheduler started")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("task scheduler stopped")
			return
		case <-ticker.C:
			s.checkAndRun(ctx)
		}
	}
}

func (s *Scheduler) checkAndRun(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, conversation_id, name, prompt, task_type,
		        cron_expression, run_at, timezone, output_config, status,
		        max_tokens, enable_tools, last_run_at, total_runs, total_tokens
		 FROM scheduled_tasks
		 WHERE status = 'active'
		 ORDER BY created_at ASC LIMIT 20`)
	if err != nil {
		slog.Error("failed to query active tasks", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var t domain.ScheduledTask
		if err := rows.Scan(&t.ID, &t.UserID, &t.ConversationID, &t.Name, &t.Prompt,
			&t.TaskType, &t.CronExpression, &t.RunAt, &t.Timezone, &t.OutputConfig, &t.Status,
			&t.MaxTokens, &t.EnableTools, &t.LastRunAt, &t.TotalRuns, &t.TotalTokens); err != nil {
			slog.Error("failed to scan task", "err", err)
			continue
		}

		if s.shouldRun(&t) {
			go s.executor.Run(ctx, &t)
		}
	}
}

func (s *Scheduler) shouldRun(t *domain.ScheduledTask) bool {
	if t.TaskType == "once" {
		if t.RunAt != nil && time.Now().After(*t.RunAt) && t.TotalRuns == 0 {
			return true
		}
		return false
	}

	if t.LastRunAt == nil {
		return true
	}

	minInterval := time.Duration(s.cfg.MinInterval) * time.Minute
	return time.Since(*t.LastRunAt) >= minInterval
}

func (s *Scheduler) CreateTask(ctx context.Context, t *domain.ScheduledTask) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	t.Status = "active"

	_, err := s.pool.Exec(ctx,
		`INSERT INTO scheduled_tasks (id, user_id, conversation_id, source_message_id,
		 name, prompt, task_type, cron_expression, run_at, timezone,
		 output_config, status, max_tokens, enable_tools, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		t.ID, t.UserID, t.ConversationID, t.SourceMessageID,
		t.Name, t.Prompt, t.TaskType, t.CronExpression, t.RunAt, t.Timezone,
		t.OutputConfig, t.Status, t.MaxTokens, t.EnableTools, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (s *Scheduler) GetUserTasks(ctx context.Context, userID uuid.UUID) ([]domain.ScheduledTask, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT t.id, t.user_id, t.conversation_id, t.source_message_id, t.name, t.prompt, t.task_type,
		        t.cron_expression, t.run_at, t.timezone, t.output_config, t.status,
		        t.max_tokens, t.enable_tools, t.last_run_at, t.last_run_status,
		        COALESCE(e.cnt, 0) AS total_runs,
		        COALESCE(e.tokens, 0) AS total_tokens,
		        t.created_at, t.updated_at
		 FROM scheduled_tasks t
		 LEFT JOIN (
		     SELECT task_id,
		            COUNT(*)::int AS cnt,
		            COALESCE(SUM(COALESCE(input_tokens,0) + COALESCE(output_tokens,0)), 0)::bigint AS tokens
		     FROM task_executions GROUP BY task_id
		 ) e ON e.task_id = t.id
		 WHERE t.user_id = $1 ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []domain.ScheduledTask
	for rows.Next() {
		var t domain.ScheduledTask
		if err := rows.Scan(&t.ID, &t.UserID, &t.ConversationID, &t.SourceMessageID,
			&t.Name, &t.Prompt, &t.TaskType, &t.CronExpression, &t.RunAt, &t.Timezone,
			&t.OutputConfig, &t.Status, &t.MaxTokens, &t.EnableTools,
			&t.LastRunAt, &t.LastRunStatus, &t.TotalRuns, &t.TotalTokens,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *Scheduler) UpdateTaskStatus(ctx context.Context, taskID uuid.UUID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE scheduled_tasks SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now(), taskID)
	return err
}

func (s *Scheduler) DeleteTask(ctx context.Context, taskID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM scheduled_tasks WHERE id = $1 AND user_id = $2`, taskID, userID)
	return err
}

func (s *Scheduler) DeleteTasksBySourceMessage(ctx context.Context, messageID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM scheduled_tasks WHERE source_message_id = $1`, messageID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ManageTool is the bridge function for the manage_scheduled_task tool.
func (s *Scheduler) ManageTool(ctx context.Context, userID fmt.Stringer, action string, input map[string]any) (string, error) {
	uid, err := uuid.Parse(userID.String())
	if err != nil {
		return "", fmt.Errorf("invalid user id")
	}

	switch action {
	case "create":
		return s.toolCreate(ctx, uid, input)
	case "list":
		return s.toolList(ctx, uid)
	case "update":
		return s.toolUpdate(ctx, uid, input)
	case "pause":
		return s.toolPauseResume(ctx, uid, input, "paused")
	case "resume":
		return s.toolPauseResume(ctx, uid, input, "active")
	case "delete":
		return s.toolDelete(ctx, uid, input)
	case "run_now":
		return s.toolRunNow(ctx, uid, input)
	default:
		return "", fmt.Errorf("未知操作: %s", action)
	}
}

func (s *Scheduler) toolCreate(ctx context.Context, userID uuid.UUID, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	prompt, _ := input["prompt"].(string)
	taskType, _ := input["task_type"].(string)
	cronExpr, _ := input["cron_expression"].(string)
	runAtStr, _ := input["run_at"].(string)
	timezone, _ := input["timezone"].(string)

	if name == "" {
		return "", fmt.Errorf("name 参数不能为空")
	}
	if prompt == "" {
		return "", fmt.Errorf("prompt 参数不能为空")
	}
	if taskType == "" {
		taskType = "recurring"
	}
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}

	var runAt *time.Time
	if taskType == "once" {
		if runAtStr == "" {
			return "", fmt.Errorf("一次性任务需要 run_at 参数")
		}
		t, err := time.Parse(time.RFC3339, runAtStr)
		if err != nil {
			loc, _ := time.LoadLocation(timezone)
			if loc == nil {
				loc = time.Local
			}
			t, err = time.ParseInLocation("2006-01-02T15:04:05", runAtStr, loc)
			if err != nil {
				return "", fmt.Errorf("run_at 时间格式无效，请使用 ISO 8601 格式")
			}
		}
		if t.Before(time.Now()) {
			return "", fmt.Errorf("执行时间不能是过去的时间")
		}
		runAt = &t
	} else {
		if cronExpr == "" {
			return "", fmt.Errorf("周期性任务需要 cron_expression 参数")
		}
	}

	// Safety check
	guard := NewSafetyGuard(s.pool, s.cfg)
	if err := guard.CheckCanCreate(ctx, userID); err != nil {
		return "", err
	}

	outputConfig := buildOutputConfig(input)

	task := &domain.ScheduledTask{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        name,
		Prompt:      prompt,
		TaskType:    taskType,
		Timezone:    timezone,
		OutputConfig: outputConfig,
		Status:      "active",
		EnableTools: true,
	}
	if cronExpr != "" {
		task.CronExpression = &cronExpr
	}
	task.RunAt = runAt
	if srcMsgStr, ok := input["_source_message_id"].(string); ok {
		if srcMsgID, err := uuid.Parse(srcMsgStr); err == nil {
			task.SourceMessageID = &srcMsgID
		}
	}

	if err := s.CreateTask(ctx, task); err != nil {
		return "", fmt.Errorf("创建任务失败: %w", err)
	}

	timeDesc := cronExpr
	if taskType == "once" && runAt != nil {
		timeDesc = formatTime(*runAt, timezone) + "（一次性）"
	} else if cronExpr != "" {
		timeDesc = formatCronDescription(cronExpr)
	}

	typeLabel := "周期性任务"
	if taskType == "once" {
		typeLabel = "一次性任务"
	}

	return fmt.Sprintf(
		"任务已创建：\n\n📋 %s\n🔄 %s\n⏰ %s（%s）\n📝 %s\n\n任务 ID: %s",
		name, typeLabel, timeDesc, timezone,
		truncate(prompt, 100), task.ID,
	), nil
}

func (s *Scheduler) toolList(ctx context.Context, userID uuid.UUID) (string, error) {
	tasks, err := s.GetUserTasks(ctx, userID)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "你目前没有定时任务。", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "你目前有 %d 个定时任务：\n\n", len(tasks))

	for i, t := range tasks {
		statusLabel := "● 运行中"
		switch t.Status {
		case "paused":
			statusLabel = "● 已暂停"
		case "completed", "stopped":
			statusLabel = "● 已完成"
		}

		timeDesc := ""
		if t.TaskType == "once" && t.RunAt != nil {
			timeDesc = formatTime(*t.RunAt, t.Timezone) + "（一次性）"
		} else if t.CronExpression != nil {
			timeDesc = formatCronDescription(*t.CronExpression)
		}

		lastRun := "尚未执行"
		if t.LastRunAt != nil {
			lastRun = formatTime(*t.LastRunAt, t.Timezone)
			if t.LastRunStatus != nil && *t.LastRunStatus == "success" {
				lastRun += " ✅"
			} else {
				lastRun += " ❌"
			}
		}

		fmt.Fprintf(&sb, "%d. 📋 %s  %s\n   ⏰ %s\n   📊 已执行 %d 次 | 消耗 %s tokens\n   上次执行: %s\n   ID: %s\n\n",
			i+1, t.Name, statusLabel, timeDesc, t.TotalRuns, formatTokens(t.TotalTokens), lastRun, t.ID)
	}

	return strings.TrimSpace(sb.String()), nil
}

func (s *Scheduler) toolUpdate(ctx context.Context, userID uuid.UUID, input map[string]any) (string, error) {
	taskID, err := parseTaskID(input)
	if err != nil {
		return "", err
	}

	task, err := s.GetTaskByIDAndUser(ctx, taskID, userID)
	if err != nil {
		return "", err
	}

	if v, ok := input["name"].(string); ok && v != "" {
		task.Name = v
	}
	if v, ok := input["prompt"].(string); ok && v != "" {
		task.Prompt = v
	}
	if v, ok := input["cron_expression"].(string); ok && v != "" {
		task.CronExpression = &v
	}
	if v, ok := input["timezone"].(string); ok && v != "" {
		task.Timezone = v
	}
	if _, ok := input["output_targets"]; ok {
		task.OutputConfig = buildOutputConfig(input)
	}
	task.UpdatedAt = time.Now()

	_, err = s.pool.Exec(ctx,
		`UPDATE scheduled_tasks SET name=$1, prompt=$2, cron_expression=$3, timezone=$4, output_config=$5, updated_at=$6 WHERE id=$7`,
		task.Name, task.Prompt, task.CronExpression, task.Timezone, task.OutputConfig, task.UpdatedAt, task.ID)
	if err != nil {
		return "", fmt.Errorf("更新任务失败: %w", err)
	}

	timeDesc := ""
	if task.CronExpression != nil {
		timeDesc = formatCronDescription(*task.CronExpression)
	}
	return fmt.Sprintf("任务已更新：\n📋 %s\n⏰ %s", task.Name, timeDesc), nil
}

func (s *Scheduler) toolPauseResume(ctx context.Context, userID uuid.UUID, input map[string]any, newStatus string) (string, error) {
	taskID, err := parseTaskID(input)
	if err != nil {
		return "", err
	}

	task, err := s.GetTaskByIDAndUser(ctx, taskID, userID)
	if err != nil {
		return "", err
	}

	if err := s.UpdateTaskStatus(ctx, taskID, newStatus); err != nil {
		return "", fmt.Errorf("操作失败: %w", err)
	}

	if newStatus == "paused" {
		return fmt.Sprintf("%s 已暂停。说\"恢复\"可以重新启动。", task.Name), nil
	}
	return fmt.Sprintf("%s 已恢复运行。", task.Name), nil
}

func (s *Scheduler) toolDelete(ctx context.Context, userID uuid.UUID, input map[string]any) (string, error) {
	taskID, err := parseTaskID(input)
	if err != nil {
		return "", err
	}

	task, err := s.GetTaskByIDAndUser(ctx, taskID, userID)
	if err != nil {
		return "", err
	}

	if err := s.DeleteTask(ctx, taskID, userID); err != nil {
		return "", fmt.Errorf("删除失败: %w", err)
	}
	return fmt.Sprintf("已删除\"%s\"任务。", task.Name), nil
}

func (s *Scheduler) toolRunNow(ctx context.Context, userID uuid.UUID, input map[string]any) (string, error) {
	taskID, err := parseTaskID(input)
	if err != nil {
		return "", err
	}

	task, err := s.GetTaskByIDAndUser(ctx, taskID, userID)
	if err != nil {
		return "", err
	}

	go s.executor.Run(ctx, task)
	return fmt.Sprintf("正在执行\"%s\"，结果将推送到设定的渠道。", task.Name), nil
}

func (s *Scheduler) GetTaskByIDAndUser(ctx context.Context, taskID, userID uuid.UUID) (*domain.ScheduledTask, error) {
	var t domain.ScheduledTask
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, conversation_id, source_message_id, name, prompt, task_type,
		        cron_expression, run_at, timezone, output_config, status,
		        max_tokens, enable_tools, last_run_at, last_run_status,
		        total_runs, total_tokens, created_at, updated_at
		 FROM scheduled_tasks WHERE id = $1 AND user_id = $2`, taskID, userID).Scan(
		&t.ID, &t.UserID, &t.ConversationID, &t.SourceMessageID,
		&t.Name, &t.Prompt, &t.TaskType, &t.CronExpression, &t.RunAt, &t.Timezone,
		&t.OutputConfig, &t.Status, &t.MaxTokens, &t.EnableTools,
		&t.LastRunAt, &t.LastRunStatus, &t.TotalRuns, &t.TotalTokens,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("任务不存在")
	}
	return &t, nil
}

func parseTaskID(input map[string]any) (uuid.UUID, error) {
	s, _ := input["task_id"].(string)
	if s == "" {
		return uuid.Nil, fmt.Errorf("task_id 参数不能为空")
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("无效的 task_id")
	}
	return id, nil
}

func buildOutputConfig(input map[string]any) json.RawMessage {
	targets, ok := input["output_targets"].([]any)
	if !ok || len(targets) == 0 {
		return json.RawMessage(`{"targets":[{"type":"conversation"}]}`)
	}
	var tList []map[string]string
	for _, t := range targets {
		if s, ok := t.(string); ok {
			tList = append(tList, map[string]string{"type": s})
		}
	}
	if len(tList) == 0 {
		return json.RawMessage(`{"targets":[{"type":"conversation"}]}`)
	}
	b, _ := json.Marshal(map[string]any{"targets": tList})
	return b
}

func formatTime(t time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

func formatCronDescription(cronExpr string) string {
	parts := strings.Fields(cronExpr)
	if len(parts) < 6 {
		return cronExpr
	}
	minute, hour, dom, _, dow := parts[1], parts[2], parts[3], parts[4], parts[5]

	var desc strings.Builder
	if dom == "*" || dom == "?" {
		switch {
		case dow == "*" || dow == "?":
			desc.WriteString("每天")
		case dow == "MON-FRI":
			desc.WriteString("工作日")
		default:
			desc.WriteString("每周" + translateDayOfWeek(dow))
		}
	} else {
		desc.WriteString("每月 " + dom + " 日")
	}

	if hour == "*" {
		desc.WriteString("每小时")
		if minute != "0" {
			desc.WriteString("第 " + minute + " 分")
		}
	} else {
		fmt.Fprintf(&desc, " %s:%s", padZero(hour), padZero(minute))
	}
	return desc.String()
}

func translateDayOfWeek(dow string) string {
	m := map[string]string{
		"MON": "一", "TUE": "二", "WED": "三",
		"THU": "四", "FRI": "五", "SAT": "六", "SUN": "日",
	}
	days := strings.Split(dow, ",")
	var parts []string
	for _, d := range days {
		if v, ok := m[strings.TrimSpace(d)]; ok {
			parts = append(parts, v)
		} else {
			parts = append(parts, strings.TrimSpace(d))
		}
	}
	return strings.Join(parts, "、")
}

func padZero(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

func formatTokens(tokens int64) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.0fK", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
