package storage

import "context"

type Service interface {
	UploadFile(ctx context.Context, conversationID, path string, data []byte) error
	ReadFile(ctx context.Context, conversationID, path string) ([]byte, error)
	DeleteFile(ctx context.Context, conversationID, path string) error
	GeneratePresignedURL(ctx context.Context, conversationID, path, method string, expireMinutes int) (string, error)
	GenerateUploadURL(ctx context.Context, conversationID, path string, expireMinutes int) (string, error)
}
