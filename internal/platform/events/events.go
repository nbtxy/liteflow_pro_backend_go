package events

import "context"

type EventType string

type Event struct {
	Type EventType      `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

func NewEvent(typ EventType, data map[string]any) Event {
	if data == nil {
		data = make(map[string]any)
	}
	data["type"] = string(typ)
	return Event{Type: typ, Data: data}
}

type Sink func(Event)

type sinkKey struct{}

func WithSink(ctx context.Context, s Sink) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, sinkKey{}, s)
}

func Emit(ctx context.Context, e Event) {
	s, _ := ctx.Value(sinkKey{}).(Sink)
	if s != nil {
		s(e)
	}
}
