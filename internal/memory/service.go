package memory

import (
	"context"
	"fmt"
	"strings"
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

func (s *Service) GetFormattedMemories(ctx context.Context, userID uuid.UUID) (string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT category, content FROM user_memories
		 WHERE user_id = $1 AND (is_active IS NULL OR is_active = TRUE)
		 ORDER BY category, created_at`, userID)
	if err != nil {
		return "", fmt.Errorf("get memories: %w", err)
	}
	defer rows.Close()

	categories := make(map[string][]string)
	for rows.Next() {
		var cat, content string
		if err := rows.Scan(&cat, &content); err != nil {
			return "", err
		}
		categories[cat] = append(categories[cat], content)
	}

	if len(categories) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("## 用户记忆\n")
	for cat, items := range categories {
		sb.WriteString(fmt.Sprintf("### %s\n", cat))
		for _, item := range items {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}
	return sb.String(), nil
}

func (s *Service) Add(ctx context.Context, userID uuid.UUID, category, content string, conversationID *uuid.UUID) error {
	confidence := float32(0.8)
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_memories (user_id, category, content, source_conversation_id, confidence, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, TRUE, $6, $6)`,
		userID, category, content, conversationID, confidence, time.Now())
	return err
}

func (s *Service) Update(ctx context.Context, memoryID uuid.UUID, content string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_memories SET content = $1, updated_at = $2 WHERE id = $3`,
		content, time.Now(), memoryID)
	return err
}

func (s *Service) Delete(ctx context.Context, memoryID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_memories SET is_active = FALSE, updated_at = $1 WHERE id = $2`,
		time.Now(), memoryID)
	return err
}

func (s *Service) HardDelete(ctx context.Context, memoryID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM user_memories WHERE id = $1`, memoryID)
	return err
}

func (s *Service) GetByUserID(ctx context.Context, userID uuid.UUID) ([]domain.UserMemory, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, category, content, source_conversation_id, confidence, is_active, created_at, updated_at
		 FROM user_memories WHERE user_id = $1 AND (is_active IS NULL OR is_active = TRUE)
		 ORDER BY category, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []domain.UserMemory
	for rows.Next() {
		var m domain.UserMemory
		if err := rows.Scan(&m.ID, &m.UserID, &m.Category, &m.Content,
			&m.SourceConversationID, &m.Confidence, &m.IsActive, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, nil
}

func (s *Service) ManageTool(ctx context.Context, userID fmt.Stringer, action string, input map[string]any) (string, error) {
	uid, err := uuid.Parse(userID.String())
	if err != nil {
		return "", fmt.Errorf("invalid user ID")
	}

	switch action {
	case "add":
		category, _ := input["category"].(string)
		content, _ := input["content"].(string)
		if category == "" || content == "" {
			return "category and content are required", nil
		}
		if err := s.Add(ctx, uid, category, content, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("Memory added: [%s] %s", category, content), nil

	case "update":
		memID, _ := input["memory_id"].(string)
		content, _ := input["content"].(string)
		if memID == "" || content == "" {
			return "memory_id and content are required", nil
		}
		mid, err := uuid.Parse(memID)
		if err != nil {
			return "invalid memory_id", nil
		}
		if err := s.Update(ctx, mid, content); err != nil {
			return "", err
		}
		return "Memory updated", nil

	case "delete":
		memID, _ := input["memory_id"].(string)
		if memID == "" {
			return "memory_id is required", nil
		}
		mid, err := uuid.Parse(memID)
		if err != nil {
			return "invalid memory_id", nil
		}
		if err := s.Delete(ctx, mid); err != nil {
			return "", err
		}
		return "Memory deleted", nil

	default:
		return fmt.Sprintf("unknown action: %s", action), nil
	}
}
