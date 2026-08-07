package authorization

import "context"

type contextKey struct{}

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, contextKey{}, userID)
}

// UserID возвращает идентификатор, положенный в контекст посредником.
func UserID(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(contextKey{}).(int64)
	return userID, ok && userID > 0
}
