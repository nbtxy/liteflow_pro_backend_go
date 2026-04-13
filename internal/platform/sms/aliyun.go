package sms

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liteflow/backend/internal/config"
)

type AliyunService struct {
	cfg config.SMSConfig
}

func NewAliyun(cfg config.SMSConfig) *AliyunService {
	return &AliyunService{cfg: cfg}
}

func (s *AliyunService) Send(_ context.Context, phone, code string) error {
	// TODO: Integrate with Alibaba Cloud SMS SDK
	// For now, log the code
	slog.Info("sending SMS via Aliyun", "phone", phone)
	_ = fmt.Sprintf(`{"code":"%s"}`, code)
	return nil
}
