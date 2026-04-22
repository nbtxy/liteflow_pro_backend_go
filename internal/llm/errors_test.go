package llm

import (
	"errors"
	"testing"
)

func TestParseProviderError_OpenAIContextExceeded(t *testing.T) {
	body := []byte(`{"error":{"code":"context_length_exceeded","message":"This model's maximum context length is 128000 tokens...","type":"invalid_request_error"}}`)
	err := ParseProviderError(400, body)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var ce *ErrContextExceeded
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ErrContextExceeded, got %T (%v)", err, err)
	}
	if ce.StatusCode != 400 {
		t.Errorf("statusCode: want 400, got %d", ce.StatusCode)
	}
	if !IsContextExceeded(err) {
		t.Errorf("IsContextExceeded should be true")
	}
}

func TestParseProviderError_DeepSeekStringTooLong(t *testing.T) {
	body := []byte(`{"error":{"code":"string_too_long","message":"content is too long","type":"invalid_request_error"}}`)
	err := ParseProviderError(400, body)
	if !IsContextExceeded(err) {
		t.Fatalf("expected context-exceeded, got %v (%T)", err, err)
	}
}

func TestParseProviderError_GenericInvalidRequestWithContextHint(t *testing.T) {
	body := []byte(`{"error":{"code":"bad_request","message":"Input exceeds the maximum context window of this model","type":"invalid_request_error"}}`)
	err := ParseProviderError(400, body)
	if !IsContextExceeded(err) {
		t.Fatalf("expected context-exceeded via message hint, got %v (%T)", err, err)
	}
}

func TestParseProviderError_OtherStructuredErrorIsStreamError(t *testing.T) {
	body := []byte(`{"error":{"code":"rate_limit_exceeded","message":"too many requests","type":"rate_limit_error"}}`)
	err := ParseProviderError(429, body)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if IsContextExceeded(err) {
		t.Errorf("rate limit should not be classified as context-exceeded")
	}
	var pe *ErrProviderStreamError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ErrProviderStreamError, got %T", err)
	}
	if pe.Code != "rate_limit_exceeded" {
		t.Errorf("code: want rate_limit_exceeded, got %q", pe.Code)
	}
}

func TestParseProviderError_EmptyOrUnrecognized(t *testing.T) {
	if err := ParseProviderError(400, nil); err != nil {
		t.Errorf("empty body should return nil, got %v", err)
	}
	if err := ParseProviderError(400, []byte(`{"not_error":"no"}`)); err != nil {
		t.Errorf("unrecognized shape should return nil, got %v", err)
	}
	if err := ParseProviderError(400, []byte(`not json`)); err != nil {
		t.Errorf("non-json should return nil, got %v", err)
	}
}

func TestErrContextExceeded_WrapsUnderlying(t *testing.T) {
	inner := errors.New("boom")
	wrapped := &ErrContextExceeded{StatusCode: 400, ErrorCode: "context_length_exceeded", Underlying: inner}
	if !errors.Is(wrapped, inner) {
		t.Errorf("errors.Is should unwrap to underlying")
	}
	if !IsContextExceeded(wrapped) {
		t.Errorf("IsContextExceeded should be true")
	}
}
