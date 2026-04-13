package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/liteflow/backend/internal/config"
)

type OSSService struct {
	cfg config.OSSConfig
}

func NewOSS(cfg config.OSSConfig) *OSSService {
	return &OSSService{cfg: cfg}
}

func (s *OSSService) objectKey(conversationID, path string) string {
	return fmt.Sprintf("conversations/%s/%s", conversationID, path)
}

func (s *OSSService) UploadFile(_ context.Context, conversationID, path string, data []byte) error {
	_ = bytes.NewReader(data)
	slog.Info("OSS upload", "key", s.objectKey(conversationID, path), "size", len(data))
	// TODO: integrate aliyun-oss-go-sdk
	return nil
}

func (s *OSSService) ReadFile(_ context.Context, conversationID, path string) ([]byte, error) {
	slog.Info("OSS read", "key", s.objectKey(conversationID, path))
	// TODO: integrate aliyun-oss-go-sdk
	return nil, fmt.Errorf("OSS read not implemented")
}

func (s *OSSService) DeleteFile(_ context.Context, conversationID, path string) error {
	slog.Info("OSS delete", "key", s.objectKey(conversationID, path))
	// TODO: integrate aliyun-oss-go-sdk
	return nil
}

func (s *OSSService) GeneratePresignedURL(_ context.Context, conversationID, path, method string, expireMinutes int) (string, error) {
	_ = time.Duration(expireMinutes) * time.Minute
	_ = io.Discard
	// TODO: generate presigned URL with aliyun-oss-go-sdk
	return fmt.Sprintf("https://%s.%s/%s", s.cfg.Bucket, s.cfg.Endpoint, s.objectKey(conversationID, path)), nil
}

func (s *OSSService) GenerateUploadURL(_ context.Context, conversationID, path string, expireMinutes int) (string, error) {
	return s.GeneratePresignedURL(nil, conversationID, path, "PUT", expireMinutes)
}
