package storage

import (
	"log/slog"

	"github.com/liteflow/backend/internal/config"
)

type STSCredentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	SecurityToken   string `json:"securityToken"`
	Expiration      string `json:"expiration"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
}

type OssStsService struct {
	cfg config.OSSConfig
}

func NewOssSts(cfg config.OSSConfig) *OssStsService {
	return &OssStsService{cfg: cfg}
}

func (s *OssStsService) GetSTSCredentials(conversationID string) (*STSCredentials, error) {
	slog.Info("generating STS credentials", "conversationId", conversationID)
	// TODO: integrate alibabacloud-go STS SDK
	return &STSCredentials{
		Region: s.cfg.RegionID,
		Bucket: s.cfg.Bucket,
	}, nil
}
