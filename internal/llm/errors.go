package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrContextExceeded indicates the LLM provider rejected a request because
// the input (context) exceeded the model's maximum allowed size.
type ErrContextExceeded struct {
	StatusCode int
	ErrorCode  string
	Message    string
	Underlying error
}

func (e *ErrContextExceeded) Error() string {
	if e.Underlying != nil {
		return fmt.Sprintf("context exceeded (status=%d code=%q): %v", e.StatusCode, e.ErrorCode, e.Underlying)
	}
	return fmt.Sprintf("context exceeded (status=%d code=%q): %s", e.StatusCode, e.ErrorCode, e.Message)
}

func (e *ErrContextExceeded) Unwrap() error { return e.Underlying }

// IsContextExceeded reports whether err is or wraps ErrContextExceeded.
func IsContextExceeded(err error) bool {
	var e *ErrContextExceeded
	return errors.As(err, &e)
}

// ErrProviderStreamError is returned when a provider sent a 200 response but
// the SSE body contains an error frame (OpenAI-compatible gateways sometimes
// do this for invalid requests that they fail to detect before streaming).
type ErrProviderStreamError struct {
	Code    string
	Type    string
	Message string
}

func (e *ErrProviderStreamError) Error() string {
	return fmt.Sprintf("provider stream error (code=%q type=%q): %s", e.Code, e.Type, e.Message)
}

// providerErrorBody is a best-effort schema covering OpenAI / DeepSeek /
// OpenRouter error response shapes.
type providerErrorBody struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// ParseProviderError inspects a response body and returns one of:
//   - *ErrContextExceeded when the error indicates context overflow
//   - *ErrProviderStreamError for any other structured provider error
//   - nil when body is not a recognized provider error payload
func ParseProviderError(statusCode int, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var parsed providerErrorBody
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error == nil {
		return nil
	}
	code := strings.ToLower(strings.TrimSpace(parsed.Error.Code))
	typ := strings.ToLower(strings.TrimSpace(parsed.Error.Type))
	msg := strings.ToLower(parsed.Error.Message)

	contextHints := []string{"context length", "maximum context", "context window", "too long", "exceeds"}
	if code == "context_length_exceeded" || code == "string_too_long" {
		return &ErrContextExceeded{StatusCode: statusCode, ErrorCode: parsed.Error.Code, Message: parsed.Error.Message}
	}
	if typ == "invalid_request_error" || code == "invalid_request_error" {
		for _, hint := range contextHints {
			if strings.Contains(msg, hint) {
				return &ErrContextExceeded{StatusCode: statusCode, ErrorCode: parsed.Error.Code, Message: parsed.Error.Message}
			}
		}
	}

	return &ErrProviderStreamError{
		Code:    parsed.Error.Code,
		Type:    parsed.Error.Type,
		Message: parsed.Error.Message,
	}
}
