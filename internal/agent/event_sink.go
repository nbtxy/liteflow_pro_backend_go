package agent

import "context"

type eventSinkKey struct{}

type EventSink func(Event)

func WithEventSink(ctx context.Context, sink EventSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, eventSinkKey{}, sink)
}

func EmitFromContext(ctx context.Context, event Event) {
	sink, _ := ctx.Value(eventSinkKey{}).(EventSink)
	if sink != nil {
		sink(event)
	}
}
