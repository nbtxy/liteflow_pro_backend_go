package feedback

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liteflow/backend/internal/domain"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) Create(ctx context.Context, fb *domain.Feedback) error {
	if fb.ID == uuid.Nil {
		fb.ID = uuid.New()
	}
	fb.CreatedAt = time.Now()

	_, err := s.pool.Exec(ctx,
		`INSERT INTO feedbacks (id, user_id, conversation_id, message_id, rating, reasons, comment, context, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT ON CONSTRAINT feedbacks_user_id_message_id_key
		 DO UPDATE SET rating = $5, reasons = $6, comment = $7, context = $8`,
		fb.ID, fb.UserID, fb.ConversationID, fb.MessageID, fb.Rating,
		fb.Reasons, fb.Comment, fb.Context, fb.CreatedAt)
	if err != nil {
		return fmt.Errorf("create feedback: %w", err)
	}
	return nil
}

func (s *Service) GetByMessageID(ctx context.Context, messageID, userID uuid.UUID) (*domain.Feedback, error) {
	var fb domain.Feedback
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, conversation_id, message_id, rating, reasons, comment, context, created_at
		 FROM feedbacks WHERE message_id = $1 AND user_id = $2`,
		messageID, userID).Scan(
		&fb.ID, &fb.UserID, &fb.ConversationID, &fb.MessageID,
		&fb.Rating, &fb.Reasons, &fb.Comment, &fb.Context, &fb.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &fb, nil
}

func (s *Service) GetByConversation(ctx context.Context, conversationID uuid.UUID) ([]domain.Feedback, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, conversation_id, message_id, rating, reasons, comment, context, created_at
		 FROM feedbacks WHERE conversation_id = $1 ORDER BY created_at DESC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fbs []domain.Feedback
	for rows.Next() {
		var fb domain.Feedback
		if err := rows.Scan(&fb.ID, &fb.UserID, &fb.ConversationID, &fb.MessageID,
			&fb.Rating, &fb.Reasons, &fb.Comment, &fb.Context, &fb.CreatedAt); err != nil {
			return nil, err
		}
		fbs = append(fbs, fb)
	}
	return fbs, nil
}
