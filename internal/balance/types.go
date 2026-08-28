package balance

import (
	"context"

	"github.com/shigabutdinoff/gophermart/internal/money"
)

// Balance хранит доступные и уже списанные баллы пользователя.
type Balance struct {
	Current   money.Points
	Withdrawn money.Points
}

// Reader считает состояние счёта пользователя из начислений и списаний.
type Reader interface {
	Balance(ctx context.Context, userID int64) (Balance, error)
}
