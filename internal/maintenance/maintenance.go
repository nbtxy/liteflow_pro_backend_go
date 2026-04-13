package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/channel"
	"github.com/liteflow/backend/internal/config"
)

type Scheduler struct {
	pool       *pgxpool.Pool
	cfg        config.Config
	channelMgr *channel.Manager
}

func NewMaintenanceScheduler(pool *pgxpool.Pool, cfg config.Config) *Scheduler {
	return &Scheduler{
		pool: pool,
		cfg:  cfg,
	}
}

func (s *Scheduler) SetChannelManager(channelMgr *channel.Manager) {
	s.channelMgr = channelMgr
}

func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("maintenance scheduler started")

	go runDailyAt(ctx, 5, 0, s.cleanupOldTaskExecutions)
	go runDailyAt(ctx, 4, 0, s.cleanupOldTokenUsages)
	go runEvery(ctx, time.Hour, s.cleanupExpiredSmsCodes)
	go runDailyAt(ctx, 3, 0, s.cleanupExpiredRefreshTokens)

	healthCheckInterval := 60 * time.Second
	if s.cfg.Feishu.HealthCheckInterval > 0 {
		healthCheckInterval = time.Duration(s.cfg.Feishu.HealthCheckInterval) * time.Millisecond
	}
	go runEvery(ctx, healthCheckInterval, s.feishuHealthCheck)

	<-ctx.Done()
	slog.Info("maintenance scheduler stopped")
}

func (s *Scheduler) cleanupOldTaskExecutions(ctx context.Context) {
	retentionDays := s.cfg.Tasks.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM task_executions WHERE created_at < now() - ($1::int * interval '1 day')`,
		retentionDays)
	if err != nil {
		slog.Error("cleanup task executions failed", "err", err)
		return
	}
	slog.Info("cleanup task executions done", "deleted", tag.RowsAffected(), "retentionDays", retentionDays)
}

func (s *Scheduler) cleanupOldTokenUsages(ctx context.Context) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM token_usages WHERE created_at < now() - interval '90 days'`)
	if err != nil {
		slog.Error("cleanup token usages failed", "err", err)
		return
	}
	slog.Info("cleanup token usages done", "deleted", tag.RowsAffected())
}

func (s *Scheduler) cleanupExpiredSmsCodes(ctx context.Context) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sms_codes WHERE created_at < now() - interval '1 hour'`)
	if err != nil {
		slog.Error("cleanup sms codes failed", "err", err)
		return
	}
	slog.Debug("cleanup sms codes done", "deleted", tag.RowsAffected())
}

func (s *Scheduler) cleanupExpiredRefreshTokens(ctx context.Context) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE expires_at < now() OR revoked = true`)
	if err != nil {
		slog.Error("cleanup refresh tokens failed", "err", err)
		return
	}
	slog.Info("cleanup refresh tokens done", "deleted", tag.RowsAffected())
}

func (s *Scheduler) feishuHealthCheck(ctx context.Context) {
	if s.channelMgr == nil {
		return
	}
	s.channelMgr.HealthCheck(ctx)
}

func runDailyAt(ctx context.Context, hour, minute int, fn func(ctx context.Context)) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			fn(ctx)
		}
	}
}

func runEvery(ctx context.Context, interval time.Duration, fn func(ctx context.Context)) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}
