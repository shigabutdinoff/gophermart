package balance

import (
	"context"
	"fmt"
)

// ListService читает историю списаний пользователя.
type ListService struct {
	withdrawals WithdrawalLister
}

// NewListService собирает чтение истории поверх хранилища.
func NewListService(withdrawals WithdrawalLister) *ListService {
	return &ListService{withdrawals: withdrawals}
}

// List отдаёт историю списаний пользователя.
func (s *ListService) List(ctx context.Context, userID int64) ([]Withdrawal, error) {
	withdrawals, err := s.withdrawals.ListWithdrawals(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list withdrawals: %w", err)
	}

	return withdrawals, nil
}
