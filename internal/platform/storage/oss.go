package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type OSSService struct {
	bucket *oss.Bucket
}

func NewOSS(endpoint, bucketName, accessKeyID, accessKeySecret string) (*OSSService, error) {
	if endpoint == "" || bucketName == "" || accessKeyID == "" || accessKeySecret == "" {
		return nil, fmt.Errorf("invalid oss config: endpoint/bucket/access key is required")
	}

	client, err := oss.New(endpoint, accessKeyID, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("create oss client: %w", err)
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("open oss bucket: %w", err)
	}
	return &OSSService{bucket: bucket}, nil
}

func (s *OSSService) UploadFile(_ context.Context, conversationID, path string, data []byte) error {
	objectKey := buildObjectKey(conversationID, path)
	if err := s.bucket.PutObject(objectKey, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("put object %s: %w", objectKey, err)
	}
	return nil
}

func (s *OSSService) ReadFile(_ context.Context, conversationID, path string) ([]byte, error) {
	objectKey := buildObjectKey(conversationID, path)
	reader, err := s.bucket.GetObject(objectKey)
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", objectKey, err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read object %s: %w", objectKey, err)
	}
	return data, nil
}

func (s *OSSService) DeleteFile(_ context.Context, conversationID, path string) error {
	objectKey := buildObjectKey(conversationID, path)
	if err := s.bucket.DeleteObject(objectKey); err != nil {
		return fmt.Errorf("delete object %s: %w", objectKey, err)
	}
	return nil
}

func (s *OSSService) GeneratePresignedURL(
	_ context.Context,
	conversationID,
	path,
	method string,
	expireMinutes int,
) (string, error) {
	objectKey := buildObjectKey(conversationID, path)
	timeout := expireDuration(expireMinutes)
	httpMethod := oss.HTTPMethod(strings.ToUpper(strings.TrimSpace(method)))
	if httpMethod == "" {
		httpMethod = oss.HTTPGet
	}
	urlStr, err := s.bucket.SignURL(objectKey, httpMethod, int64(timeout.Seconds()))
	if err != nil {
		return "", fmt.Errorf("sign url %s: %w", objectKey, err)
	}
	return normalizeSignedURL(urlStr)
}

func (s *OSSService) GenerateUploadURL(_ context.Context, conversationID, path string, expireMinutes int) (string, error) {
	objectKey := buildObjectKey(conversationID, path)
	timeout := expireDuration(expireMinutes)
	urlStr, err := s.bucket.SignURL(objectKey, "PUT", int64(timeout.Seconds()))
	if err != nil {
		return "", fmt.Errorf("sign upload url %s: %w", objectKey, err)
	}
	return normalizeSignedURL(urlStr)
}

func buildObjectKey(conversationID, path string) string {
	cleanPath := strings.TrimPrefix(path, "/")
	return fmt.Sprintf("conversations/%s/%s", conversationID, cleanPath)
}

func expireDuration(expireMinutes int) time.Duration {
	if expireMinutes <= 0 {
		expireMinutes = 60
	}
	return time.Duration(expireMinutes) * time.Minute
}

func normalizeSignedURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse signed url: %w", err)
	}
	return parsed.String(), nil
}
