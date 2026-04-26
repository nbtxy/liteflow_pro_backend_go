package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/dysmsapi"
	"github.com/liteflow/backend/internal/config"
)

type AliyunService struct {
	cfg config.SMSConfig
}

func NewAliyun(cfg config.SMSConfig) *AliyunService {
	return &AliyunService{cfg: cfg}
}

func (s *AliyunService) Send(ctx context.Context, phone, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}

	client, err := dysmsapi.NewClientWithAccessKey("cn-hangzhou", s.cfg.AliyunKeyID, s.cfg.AliyunSecret)
	if err != nil {
		return fmt.Errorf("create aliyun sms client: %w", err)
	}

	paramBytes, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return fmt.Errorf("marshal aliyun sms template param: %w", err)
	}

	req := dysmsapi.CreateSendSmsRequest()
	req.Scheme = "https"
	if ep := strings.TrimSpace(s.cfg.AliyunEndpoint); ep != "" {
		req.Domain = ep
	}
	req.PhoneNumbers = phone
	req.SignName = s.cfg.SignName
	req.TemplateCode = s.cfg.TemplateCode
	req.TemplateParam = string(paramBytes)

	resp, err := client.SendSms(req)
	if err != nil {
		return fmt.Errorf("aliyun sms send failed: %w", err)
	}
	if resp == nil {
		return errors.New("aliyun sms send failed: empty response")
	}
	if !strings.EqualFold(resp.Code, "OK") {
		return fmt.Errorf("aliyun sms send rejected: code=%s message=%s requestId=%s bizId=%s",
			resp.Code, resp.Message, resp.RequestId, resp.BizId)
	}
	return nil
}

func (s *AliyunService) validate() error {
	if strings.TrimSpace(s.cfg.AliyunKeyID) == "" {
		return errors.New("aliyun sms config missing ALIYUN_SMS_KEY_ID")
	}
	if strings.TrimSpace(s.cfg.AliyunSecret) == "" {
		return errors.New("aliyun sms config missing ALIYUN_SMS_KEY_SECRET")
	}
	if strings.TrimSpace(s.cfg.TemplateCode) == "" {
		return errors.New("aliyun sms config missing ALIYUN_SMS_TEMPLATE_CODE")
	}
	if strings.TrimSpace(s.cfg.SignName) == "" {
		return errors.New("aliyun sms config missing ALIYUN_SMS_SIGN_NAME")
	}
	return nil
}
