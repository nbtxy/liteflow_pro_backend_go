package imagegen

import "context"

// TextDeltaSink receives streaming text deltas produced during image generation.
type TextDeltaSink func(delta string)

// Provider is the interface for image generation providers.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req *Request, textSink TextDeltaSink) (*Response, error)
}

