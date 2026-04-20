package agent

import (
	"context"

	"github.com/liteflow/backend/internal/platform/events"
)

type EventSink = events.Sink

func WithEventSink(ctx context.Context, sink EventSink) context.Context {
	return events.WithSink(ctx, sink)
}

func EmitFromContext(ctx context.Context, event Event) {
	events.Emit(ctx, event)
}
