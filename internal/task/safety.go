package task

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/config"
	"github.com/robfig/cron/v3"
)

type SafetyGuard struct {
	pool *pgxpool.Pool
	cfg  config.TasksConfig
}

func NewSafetyGuard(pool *pgxpool.Pool, cfg config.TasksConfig) *SafetyGuard {
	return &SafetyGuard{pool: pool, cfg: cfg}
}

func (g *SafetyGuard) CheckCanCreate(ctx context.Context, userID uuid.UUID) error {
	var count int
	err := g.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1 AND status IN ('active', 'paused')`,
		userID).Scan(&count)
	if err != nil {
		slog.Error("check can create failed", "userId", userID.String(), "err", err)
		return fmt.Errorf("check task count: %w", err)
	}
	if count >= g.cfg.MaxPerUser {
		return fmt.Errorf("已达到最大任务数限制 (%d)", g.cfg.MaxPerUser)
	}
	return nil
}

func (g *SafetyGuard) CheckCanRun(ctx context.Context, userID uuid.UUID) error {
	var dailyRuns int
	err := g.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM task_executions e
		 JOIN scheduled_tasks t ON e.task_id = t.id
		 WHERE t.user_id = $1 AND e.created_at > now() - interval '24 hours'`,
		userID).Scan(&dailyRuns)
	if err != nil {
		return fmt.Errorf("check daily runs: %w", err)
	}
	if dailyRuns >= g.cfg.MaxRunsPerDay {
		return fmt.Errorf("已达到每日最大执行次数限制 (%d)", g.cfg.MaxRunsPerDay)
	}

	var dailyTokens int64
	g.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(COALESCE(e.input_tokens, 0) + COALESCE(e.output_tokens, 0)), 0)
		 FROM task_executions e
		 JOIN scheduled_tasks t ON e.task_id = t.id
		 WHERE t.user_id = $1 AND e.created_at > now() - interval '24 hours'`,
		userID).Scan(&dailyTokens)
	if dailyTokens >= int64(g.cfg.MaxDailyTokens) {
		return fmt.Errorf("已达到每日Token用量限制")
	}

	return nil
}

func (g *SafetyGuard) ValidateCronExpression(cronExpr string, minIntervalMin int) error {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(cronExpr)
	if err != nil {
		return fmt.Errorf("cron 表达式格式无效，请使用 6 位格式（秒 分 时 日 月 周）")
	}

	now := time.Now()
	first := schedule.Next(now)
	if first.IsZero() {
		return fmt.Errorf("cron 表达式无有效触发时间")
	}
	second := schedule.Next(first)
	if second.IsZero() {
		return fmt.Errorf("cron 表达式无有效触发时间")
	}

	minInterval := time.Duration(minIntervalMin) * time.Minute
	if second.Sub(first) < minInterval {
		return fmt.Errorf("任务执行间隔不能小于 %d 分钟", minIntervalMin)
	}
	return nil
}
