package api

import (
	"context"
)

type contextKey string

const UserIDKey contextKey = "user_id"

func GetUserID(ctx context.Context) int64 {
	if id, ok := ctx.Value(UserIDKey).(int64); ok {
		return id
	}
	return 0
}
