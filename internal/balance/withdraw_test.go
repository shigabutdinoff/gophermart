package balance_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/balance"
	balancemocks "github.com/shigabutdinoff/gophermart/internal/balance/mocks"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

func TestWithdrawNormalizesAndStoresRequest(t *testing.T) {
	storage := balancemocks.NewMockWithdrawer(t)
	ctx := context.WithValue(context.Background(), withdrawContextKey{}, "value")
	storage.EXPECT().Withdraw(ctx, int64(42), "2377225624", money.Points(75100)).
		Return(balance.Withdrawn, nil).
		Once()

	err := balance.NewWithdrawService(storage).Withdraw(ctx, " 2377225624\n", money.Points(75100), 42)

	require.NoError(t, err)
}

func TestWithdrawRejectsOrderNumberBeforeStorage(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   error
	}{
		{name: "empty", number: " \n", want: ordernumber.ErrEmpty},
		{name: "invalid", number: "2377225625", want: ordernumber.ErrInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := balancemocks.NewMockWithdrawer(t)

			err := balance.NewWithdrawService(storage).Withdraw(context.Background(), test.number, money.Points(100), 42)

			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestWithdrawRejectsNonPositiveSumBeforeStorage(t *testing.T) {
	for _, sum := range []money.Points{0, -1} {
		t.Run(fmt.Sprint(sum), func(t *testing.T) {
			storage := balancemocks.NewMockWithdrawer(t)

			err := balance.NewWithdrawService(storage).Withdraw(context.Background(), "2377225624", sum, 42)

			require.ErrorIs(t, err, balance.ErrNonPositiveSum)
		})
	}
}

func TestWithdrawMapsStorageOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		outcome balance.WithdrawOutcome
		want    error
	}{
		{name: "withdrawn", outcome: balance.Withdrawn},
		{name: "same order repeated", outcome: balance.AlreadyWithdrawn},
		{name: "not enough funds", outcome: balance.NotEnoughFunds, want: balance.ErrInsufficientFunds},
		{name: "order taken", outcome: balance.TakenByAnother, want: balance.ErrOrderTaken},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := balancemocks.NewMockWithdrawer(t)
			ctx := context.Background()
			storage.EXPECT().Withdraw(ctx, int64(42), "2377225624", money.Points(100)).
				Return(test.outcome, nil).
				Once()

			err := balance.NewWithdrawService(storage).Withdraw(
				ctx,
				"2377225624",
				money.Points(100),
				42,
			)

			if test.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.want)
			}
		})
	}
}

func TestWithdrawRejectsUnknownStorageOutcome(t *testing.T) {
	storage := balancemocks.NewMockWithdrawer(t)
	ctx := context.Background()
	storage.EXPECT().Withdraw(ctx, int64(42), "2377225624", money.Points(100)).
		Return(balance.WithdrawOutcome(255), nil).
		Once()

	err := balance.NewWithdrawService(storage).Withdraw(
		ctx,
		"2377225624",
		money.Points(100),
		42,
	)

	assert.EqualError(t, err, "withdraw balance: unknown outcome 255")
}

func TestWithdrawWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")
	storage := balancemocks.NewMockWithdrawer(t)
	ctx := context.Background()
	storage.EXPECT().Withdraw(ctx, int64(42), "2377225624", money.Points(100)).
		Return(balance.WithdrawOutcome(0), storageErr).
		Once()

	err := balance.NewWithdrawService(storage).Withdraw(
		ctx,
		"2377225624",
		money.Points(100),
		42,
	)

	require.ErrorIs(t, err, storageErr)
	assert.ErrorContains(t, err, "withdraw balance")
}

type withdrawContextKey struct{}
