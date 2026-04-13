package task

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/config"
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
