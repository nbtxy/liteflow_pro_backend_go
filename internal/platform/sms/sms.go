package sms

import "context"

type Service interface {
	Send(ctx context.Context, phone, code string) error
}
