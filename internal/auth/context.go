package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type contextKey string

const userIDKey contextKey = "userId"

var ErrNoUserID = errors.New("no user ID in context")

func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

func UserIDFromContext(ctx context.Context) (uuid.UUID, error) {
	v := ctx.Value(userIDKey)
	if v == nil {
		return uuid.Nil, ErrNoUserID
	}
	id, ok := v.(uuid.UUID)
	if !ok {
		return uuid.Nil, ErrNoUserID
	}
	return id, nil
}

func GetUserID(ctx context.Context) (uuid.UUID, error) {
	return UserIDFromContext(ctx)
}

func MustUserID(ctx context.Context) uuid.UUID {
	id, err := UserIDFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return id
}
