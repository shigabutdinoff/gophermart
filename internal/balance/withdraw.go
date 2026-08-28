package balance

import (
	"context"
	"fmt"

	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

// WithdrawService проверяет запрос и переводит исход хранилища в доменную ошибку.
type WithdrawService struct {
	withdrawals Withdrawer
}

// NewWithdrawService собирает списание поверх хранилища.
func NewWithdrawService(withdrawals Withdrawer) *WithdrawService {
	return &WithdrawService{withdrawals: withdrawals}
}

// Withdraw нормализует номер, проверяет его и сумму перед списанием.
func (s *WithdrawService) Withdraw(
	ctx context.Context,
	number string,
	sum money.Points,
	userID int64,
) error {
	number, err := ordernumber.Parse(number)
	if err != nil {
		return err
	}
	if sum <= 0 {
		return ErrNonPositiveSum
	}

	outcome, err := s.withdrawals.Withdraw(ctx, userID, number, sum)
	if err != nil {
		return fmt.Errorf("withdraw balance: %w", err)
	}

	switch outcome {
	case Withdrawn, AlreadyWithdrawn:
		return nil
	case NotEnoughFunds:
		return ErrInsufficientFunds
	case TakenByAnother:
		return ErrOrderTaken
	default:
		return fmt.Errorf("withdraw balance: unknown outcome %d", outcome)
	}
}
