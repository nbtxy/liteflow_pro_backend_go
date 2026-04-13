package sms

import (
	"context"
	"log/slog"
)

type MockService struct{}

func NewMock() *MockService {
	return &MockService{}
}

func (s *MockService) Send(_ context.Context, phone, code string) error {
	slog.Info("[MOCK SMS] verification code", "phone", phone, "code", code)
	return nil
}
