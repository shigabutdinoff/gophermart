package balance

import (
	"context"
	"fmt"
)

// ReadService читает производный счёт пользователя.
type ReadService struct {
	balances Reader
}

// NewReadService собирает чтение счёта поверх его хранилища.
func NewReadService(balances Reader) *ReadService {
	return &ReadService{balances: balances}
}

// Read отдаёт текущее состояние счёта пользователя.
func (s *ReadService) Read(ctx context.Context, userID int64) (Balance, error) {
	balance, err := s.balances.Balance(ctx, userID)
	if err != nil {
		return Balance{}, fmt.Errorf("read balance: %w", err)
	}

	return balance, nil
}
