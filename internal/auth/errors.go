package auth

import "errors"

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrInvalidCode  = errors.New("invalid verification code")
	ErrTooFrequent  = errors.New("request too frequent")
	ErrDailyLimit   = errors.New("daily limit reached")
)
