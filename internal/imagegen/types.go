package imagegen

import "github.com/liteflow/backend/internal/llm"

// InputImage is an image input for image generation/editing.
// Either URL (preferred for providers that accept HTTPS, e.g. OpenRouter) or
// Data (raw bytes, used by providers that require inline base64) should be set.
type InputImage struct {
	MimeType string
	Data     []byte
	URL      string
}

// Request is a provider-agnostic image generation request.
type Request struct {
	Prompt          string
	AspectRatio     string
	ImageCount      int
	Resolution      string
	Model           string
	ReferenceImages []InputImage
}

// Response is a provider-agnostic image generation response.
type Response struct {
	ImageBytes []byte
	MimeType   string
	Usage      *llm.LlmUsage
}

