package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

func (s *Service) Create(ctx context.Context, a *domain.Artifact) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO artifacts (id, conversation_id, message_id, file_path, type, title,
		 file_size, version, parent_id, storage_type, upload_status, metadata, file_deleted, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		a.ID, a.ConversationID, a.MessageID, a.FilePath, a.Type, a.Title,
		a.FileSize, a.Version, a.ParentID, a.StorageType, a.UploadStatus,
		a.Metadata, a.FileDeleted, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	return nil
}

func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*domain.Artifact, error) {
	var a domain.Artifact
	err := s.pool.QueryRow(ctx,
		`SELECT id, conversation_id, message_id, file_path, type, title,
		        file_size, version, parent_id, storage_type, upload_status, metadata, file_deleted, created_at
		 FROM artifacts WHERE id = $1`, id).Scan(
		&a.ID, &a.ConversationID, &a.MessageID, &a.FilePath, &a.Type, &a.Title,
		&a.FileSize, &a.Version, &a.ParentID, &a.StorageType, &a.UploadStatus,
		&a.Metadata, &a.FileDeleted, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Service) GetByConversation(ctx context.Context, conversationID uuid.UUID) ([]domain.Artifact, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, conversation_id, message_id, file_path, type, title,
		        file_size, version, parent_id, storage_type, upload_status, metadata, file_deleted, created_at
		 FROM artifacts WHERE conversation_id = $1 ORDER BY created_at DESC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []domain.Artifact
	for rows.Next() {
		var a domain.Artifact
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.MessageID, &a.FilePath, &a.Type, &a.Title,
			&a.FileSize, &a.Version, &a.ParentID, &a.StorageType, &a.UploadStatus,
			&a.Metadata, &a.FileDeleted, &a.CreatedAt); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}

func (s *Service) GetLatestArtifacts(ctx context.Context, conversationID uuid.UUID) ([]domain.Artifact, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (file_path) id, conversation_id, message_id, file_path, type, title,
		        file_size, version, parent_id, storage_type, upload_status, metadata, file_deleted, created_at
		 FROM artifacts
		 WHERE conversation_id = $1 AND (file_deleted IS NULL OR file_deleted = FALSE)
		 ORDER BY file_path, version DESC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get latest artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []domain.Artifact
	for rows.Next() {
		var a domain.Artifact
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.MessageID, &a.FilePath, &a.Type, &a.Title,
			&a.FileSize, &a.Version, &a.ParentID, &a.StorageType, &a.UploadStatus,
			&a.Metadata, &a.FileDeleted, &a.CreatedAt); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}

func (s *Service) GetVersionHistory(ctx context.Context, artifactID uuid.UUID) ([]domain.Artifact, error) {
	var rootPath string
	var convID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT file_path, conversation_id FROM artifacts WHERE id = $1`, artifactID).Scan(&rootPath, &convID)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, conversation_id, message_id, file_path, type, title,
		        file_size, version, parent_id, storage_type, upload_status, metadata, file_deleted, created_at
		 FROM artifacts
		 WHERE conversation_id = $1 AND file_path = $2
		 ORDER BY version DESC`, convID, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []domain.Artifact
	for rows.Next() {
		var a domain.Artifact
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.MessageID, &a.FilePath, &a.Type, &a.Title,
			&a.FileSize, &a.Version, &a.ParentID, &a.StorageType, &a.UploadStatus,
			&a.Metadata, &a.FileDeleted, &a.CreatedAt); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE artifacts SET file_deleted = true WHERE id = $1`, id)
	return err
}

func (s *Service) DeleteByPath(ctx context.Context, conversationID uuid.UUID, filePath string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE artifacts
		 SET file_deleted = true
		 WHERE conversation_id = $1 AND file_path = $2`,
		conversationID, filePath)
	return err
}

func (s *Service) UpdateMetadataByID(ctx context.Context, artifactID uuid.UUID, patch map[string]any) error {
	if artifactID == uuid.Nil || len(patch) == 0 {
		return nil
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal artifact metadata patch: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE artifacts
		 SET metadata = COALESCE(metadata, '{}'::jsonb) || $2::jsonb
		 WHERE id = $1`,
		artifactID, patchBytes)
	if err != nil {
		return fmt.Errorf("update artifact metadata: %w", err)
	}
	return nil
}

func (s *Service) CreateFileArtifact(ctx context.Context, conversationID, messageID uuid.UUID,
	path, content string) (map[string]any, error) {

	var existingID uuid.UUID
	var existingVersion int32
	err := s.pool.QueryRow(ctx,
		`SELECT id, version FROM artifacts
		 WHERE conversation_id = $1 AND file_path = $2 AND (file_deleted IS NULL OR file_deleted = FALSE)
		 ORDER BY version DESC LIMIT 1`, conversationID, path).Scan(&existingID, &existingVersion)

	newArtifact := &domain.Artifact{
		ID:             uuid.New(),
		ConversationID: conversationID,
		MessageID:      &messageID,
		FilePath:       path,
		Type:           string(domain.ArtifactTypeFile),
		Version:        1,
		Metadata:       json.RawMessage(`{"source":"generated"}`),
		CreatedAt:      time.Now(),
	}

	fileSize := int64(len(content))
	newArtifact.FileSize = &fileSize
	title := path
	newArtifact.Title = &title

	if err == nil {
		newArtifact.Version = existingVersion + 1
		newArtifact.ParentID = &existingID
	}

	if err := s.Create(ctx, newArtifact); err != nil {
		slog.Error("failed to create artifact", "err", err)
		return nil, err
	}

	return map[string]any{
		"artifact_id": newArtifact.ID.String(),
		"type":        newArtifact.Type,
		"title":       path,
		"version":     newArtifact.Version,
		"file_size":   fileSize,
		"parent_id": func() string {
			if newArtifact.ParentID != nil {
				return newArtifact.ParentID.String()
			}
			return ""
		}(),
	}, nil
}

func (s *Service) CreateImageArtifact(ctx context.Context, conversationID, messageID uuid.UUID,
	path string, data []byte, mimeType string) (map[string]any, error) {

	var existingID uuid.UUID
	var existingVersion int32
	err := s.pool.QueryRow(ctx,
		`SELECT id, version FROM artifacts
		 WHERE conversation_id = $1 AND file_path = $2 AND (file_deleted IS NULL OR file_deleted = FALSE)
		 ORDER BY version DESC LIMIT 1`, conversationID, path).Scan(&existingID, &existingVersion)

	if mimeType == "" {
		mimeType = "image/png"
	}
	metaBytes, _ := json.Marshal(map[string]any{
		"source":    "generated",
		"mime_type": mimeType,
	})

	newArtifact := &domain.Artifact{
		ID:             uuid.New(),
		ConversationID: conversationID,
		MessageID:      &messageID,
		FilePath:       path,
		Type:           string(domain.ArtifactTypeImage),
		Version:        1,
		Metadata:       json.RawMessage(metaBytes),
		CreatedAt:      time.Now(),
	}

	fileSize := int64(len(data))
	newArtifact.FileSize = &fileSize
	title := path
	newArtifact.Title = &title

	if err == nil {
		newArtifact.Version = existingVersion + 1
		newArtifact.ParentID = &existingID
	}

	if err := s.Create(ctx, newArtifact); err != nil {
		slog.Error("failed to create image artifact", "err", err)
		return nil, err
	}

	return map[string]any{
		"artifact_id": newArtifact.ID.String(),
		"type":        newArtifact.Type,
		"title":       path,
		"version":     newArtifact.Version,
		"file_size":   fileSize,
		"mime_type":   mimeType,
		"parent_id": func() string {
			if newArtifact.ParentID != nil {
				return newArtifact.ParentID.String()
			}
			return ""
		}(),
	}, nil
}

func (s *Service) CreateVideoArtifact(ctx context.Context, conversationID, messageID uuid.UUID,
	path string, data []byte, mimeType string) (map[string]any, error) {

	var existingID uuid.UUID
	var existingVersion int32
	err := s.pool.QueryRow(ctx,
		`SELECT id, version FROM artifacts
		 WHERE conversation_id = $1 AND file_path = $2 AND (file_deleted IS NULL OR file_deleted = FALSE)
		 ORDER BY version DESC LIMIT 1`, conversationID, path).Scan(&existingID, &existingVersion)

	if mimeType == "" {
		mimeType = "video/mp4"
	}
	metaBytes, _ := json.Marshal(map[string]any{
		"source":     "generated",
		"mime_type":  mimeType,
		"media_type": "video",
	})

	newArtifact := &domain.Artifact{
		ID:             uuid.New(),
		ConversationID: conversationID,
		MessageID:      &messageID,
		FilePath:       path,
		Type:           string(domain.ArtifactTypeFile),
		Version:        1,
		Metadata:       json.RawMessage(metaBytes),
		CreatedAt:      time.Now(),
	}

	fileSize := int64(len(data))
	newArtifact.FileSize = &fileSize
	title := path
	newArtifact.Title = &title

	if err == nil {
		newArtifact.Version = existingVersion + 1
		newArtifact.ParentID = &existingID
	}

	if err := s.Create(ctx, newArtifact); err != nil {
		slog.Error("failed to create video artifact", "err", err)
		return nil, err
	}

	return map[string]any{
		"artifact_id": newArtifact.ID.String(),
		"type":        newArtifact.Type,
		"title":       path,
		"version":     newArtifact.Version,
		"file_size":   fileSize,
		"mime_type":   mimeType,
		"parent_id": func() string {
			if newArtifact.ParentID != nil {
				return newArtifact.ParentID.String()
			}
			return ""
		}(),
	}, nil
}
