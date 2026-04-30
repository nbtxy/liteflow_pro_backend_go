package llm

import "context"

type CallContext struct {
	ConversationID string `json:"conversation_id"`
	UserMessageID  string `json:"user_message_id"`
	CallSeq        int    `json:"call_seq"`
}

type callContextKey struct{}

func WithCallContext(ctx context.Context, cc CallContext) context.Context {
	return context.WithValue(ctx, callContextKey{}, cc)
}

func CallContextFromCtx(ctx context.Context) (CallContext, bool) {
	if ctx == nil {
		return CallContext{}, false
	}
	cc, ok := ctx.Value(callContextKey{}).(CallContext)
	return cc, ok
}
