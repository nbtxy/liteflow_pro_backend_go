package memory

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ExtractionScheduler struct {
	pool      *pgxpool.Pool
	extractor *Extractor
}

func NewExtractionScheduler(pool *pgxpool.Pool, extractor *Extractor) *ExtractionScheduler {
	return &ExtractionScheduler{
		pool:      pool,
		extractor: extractor,
	}
}

func (s *ExtractionScheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	slog.Info("memory extraction scheduler started")

	for {
		select {
		case <-ctx.Done():
			slog.Info("memory extraction scheduler stopped")
			return
		case <-ticker.C:
			s.processUnextracted(ctx)
		}
	}
}

func (s *ExtractionScheduler) processUnextracted(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.user_id FROM conversations c
		 WHERE c.memory_extracted IS NULL OR c.memory_extracted = FALSE
		 AND c.updated_at < now() - interval '5 minutes'
		 LIMIT 10`)
	if err != nil {
		slog.Error("failed to find unextracted conversations", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var convID, userID [16]byte
		if err := rows.Scan(&convID, &userID); err != nil {
			continue
		}

		slog.Info("processing memory extraction", "conversationId", convID)

		// Mark as extracted
		s.pool.Exec(ctx,
			`UPDATE conversations SET memory_extracted = TRUE WHERE id = $1`, convID)
	}
}
