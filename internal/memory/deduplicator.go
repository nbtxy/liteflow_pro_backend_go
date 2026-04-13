package memory

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

type Deduplicator struct {
	memorySvc *Service
}

func NewDeduplicator(memorySvc *Service) *Deduplicator {
	return &Deduplicator{memorySvc: memorySvc}
}

func (d *Deduplicator) Deduplicate(ctx context.Context, userID uuid.UUID) (int, error) {
	memories, err := d.memorySvc.GetByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}

	removed := 0
	seen := make(map[string]uuid.UUID)

	for _, m := range memories {
		key := strings.ToLower(strings.TrimSpace(m.Category + ":" + m.Content))
		if existingID, ok := seen[key]; ok {
			slog.Debug("removing duplicate memory", "id", m.ID, "duplicateOf", existingID)
			if err := d.memorySvc.Delete(ctx, m.ID); err != nil {
				slog.Error("failed to delete duplicate memory", "err", err)
				continue
			}
			removed++
		} else {
			seen[key] = m.ID
		}
	}

	if removed > 0 {
		slog.Info("deduplicated memories", "userId", userID, "removed", removed)
	}
	return removed, nil
}
