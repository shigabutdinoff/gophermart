package balance

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

type fakeWithdrawer struct {
	outcome WithdrawOutcome
	err     error
	ctx     context.Context
	userID  int64
	number  string
	sum     money.Points
	calls   int
}

func (w *fakeWithdrawer) Withdraw(
	ctx context.Context,
	userID int64,
	number string,
	sum money.Points,
) (WithdrawOutcome, error) {
	w.calls++
	w.ctx = ctx
	w.userID = userID
	w.number = number
	w.sum = sum

	return w.outcome, w.err
}

func TestWithdrawNormalizesAndStoresRequest(t *testing.T) {
	storage := &fakeWithdrawer{outcome: Withdrawn}
	ctx := context.WithValue(context.Background(), withdrawContextKey{}, "value")

	err := NewWithdrawService(storage).Withdraw(ctx, " 2377225624\n", money.Points(75100), 42)

	require.NoError(t, err)
	assert.Same(t, ctx, storage.ctx)
	assert.Equal(t, int64(42), storage.userID)
	assert.Equal(t, "2377225624", storage.number)
	assert.Equal(t, money.Points(75100), storage.sum)
	assert.Equal(t, 1, storage.calls)
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
			storage := &fakeWithdrawer{}

			err := NewWithdrawService(storage).Withdraw(context.Background(), test.number, money.Points(100), 42)

			require.ErrorIs(t, err, test.want)
			assert.Zero(t, storage.calls)
		})
	}
}

func TestWithdrawRejectsNonPositiveSumBeforeStorage(t *testing.T) {
	for _, sum := range []money.Points{0, -1} {
		t.Run(fmt.Sprint(sum), func(t *testing.T) {
			storage := &fakeWithdrawer{}

			err := NewWithdrawService(storage).Withdraw(context.Background(), "2377225624", sum, 42)

			require.ErrorIs(t, err, ErrNonPositiveSum)
			assert.Zero(t, storage.calls)
		})
	}
}

func TestWithdrawMapsStorageOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		outcome WithdrawOutcome
		want    error
	}{
		{name: "withdrawn", outcome: Withdrawn},
		{name: "not enough funds", outcome: NotEnoughFunds, want: ErrInsufficientFunds},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewWithdrawService(&fakeWithdrawer{outcome: test.outcome}).Withdraw(
				context.Background(),
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
	err := NewWithdrawService(&fakeWithdrawer{outcome: WithdrawOutcome(255)}).Withdraw(
		context.Background(),
		"2377225624",
		money.Points(100),
		42,
	)

	assert.EqualError(t, err, "withdraw balance: unknown outcome 255")
}

func TestWithdrawWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")

	err := NewWithdrawService(&fakeWithdrawer{err: storageErr}).Withdraw(
		context.Background(),
		"2377225624",
		money.Points(100),
		42,
	)

	require.ErrorIs(t, err, storageErr)
	assert.ErrorContains(t, err, "withdraw balance")
}

type withdrawContextKey struct{}
