package balance

import (
	"context"
	"errors"
	"time"

	"github.com/shigabutdinoff/gophermart/internal/money"
)

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrNonPositiveSum    = errors.New("withdrawal sum must be positive")
)

// Balance хранит доступные и уже списанные баллы пользователя.
type Balance struct {
	Current   money.Points
	Withdrawn money.Points
}

// Withdrawal хранит запись о списании баллов пользователя.
type Withdrawal struct {
	Order       string
	Sum         money.Points
	ProcessedAt time.Time
}

// Reader считает состояние счёта пользователя из начислений и списаний.
type Reader interface {
	Balance(ctx context.Context, userID int64) (Balance, error)
}

// WithdrawalLister читает историю списаний пользователя.
type WithdrawalLister interface {
	ListWithdrawals(ctx context.Context, userID int64) ([]Withdrawal, error)
}

// WithdrawOutcome классифицирует попытку записать списание.
type WithdrawOutcome uint8

const (
	_ WithdrawOutcome = iota
	Withdrawn
	NotEnoughFunds
)

// Withdrawer атомарно списывает баллы или классифицирует отказ.
type Withdrawer interface {
	Withdraw(
		ctx context.Context,
		userID int64,
		number string,
		sum money.Points,
	) (WithdrawOutcome, error)
}
