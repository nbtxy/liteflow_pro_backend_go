package memory

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/liteflow/backend/internal/domain"
)

type Retriever struct {
	memorySvc *Service
}

func NewRetriever(memorySvc *Service) *Retriever {
	return &Retriever{memorySvc: memorySvc}
}

func (r *Retriever) Retrieve(ctx context.Context, userID uuid.UUID, query string) ([]domain.UserMemory, error) {
	all, err := r.memorySvc.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if query == "" {
		return all, nil
	}

	queryLower := strings.ToLower(query)
	var matched []domain.UserMemory
	for _, m := range all {
		if strings.Contains(strings.ToLower(m.Content), queryLower) ||
			strings.Contains(strings.ToLower(m.Category), queryLower) {
			matched = append(matched, m)
		}
	}

	return matched, nil
}
