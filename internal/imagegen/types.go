package imagegen

import "github.com/liteflow/backend/internal/llm"

// InputImage is a raw image input for image generation/editing.
type InputImage struct {
	MimeType string
	Data     []byte
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

