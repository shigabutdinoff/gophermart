package authorization

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

type contextKey struct{}

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, contextKey{}, userID)
}

// UserID возвращает идентификатор, положенный в контекст посредником.
func UserID(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(contextKey{}).(int64)
	return userID, ok && userID > 0
}

// RequireUserID отдаёт идентификатор из контекста или готовый отказ 401.
func RequireUserID(ctx context.Context) (int64, error) {
	userID, ok := UserID(ctx)
	if !ok {
		return 0, huma.Error401Unauthorized(message.Unauthorized)
	}

	return userID, nil
}
