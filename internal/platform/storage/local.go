package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type LocalService struct {
	basePath string
}

func NewLocal(basePath string) *LocalService {
	return &LocalService{basePath: basePath}
}

func (s *LocalService) UploadFile(_ context.Context, conversationID, path string, data []byte) error {
	fullPath := filepath.Join(s.basePath, "conversations", conversationID, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(fullPath, data, 0o644)
}

func (s *LocalService) ReadFile(_ context.Context, conversationID, path string) ([]byte, error) {
	fullPath := filepath.Join(s.basePath, "conversations", conversationID, path)
	return os.ReadFile(fullPath)
}

func (s *LocalService) DeleteFile(_ context.Context, conversationID, path string) error {
	fullPath := filepath.Join(s.basePath, "conversations", conversationID, path)
	return os.Remove(fullPath)
}

func (s *LocalService) DeleteConversation(_ context.Context, conversationID string) error {
	fullPath := filepath.Join(s.basePath, "conversations", conversationID)
	return os.RemoveAll(fullPath)
}

func (s *LocalService) GeneratePresignedURL(_ context.Context, conversationID, path, _ string, _ int) (string, error) {
	return fmt.Sprintf("/api/files/%s/%s", conversationID, path), nil
}

func (s *LocalService) GenerateUploadURL(_ context.Context, conversationID, path string, _ int) (string, error) {
	return fmt.Sprintf("/api/files/%s/%s", conversationID, path), nil
}
