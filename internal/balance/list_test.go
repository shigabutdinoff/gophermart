package balance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/balance"
	balancemocks "github.com/shigabutdinoff/gophermart/internal/balance/mocks"
	"github.com/shigabutdinoff/gophermart/internal/money"
)

func TestListReturnsUserWithdrawalsWithoutChanges(t *testing.T) {
	ctx := context.WithValue(context.Background(), listContextKey{}, "value")
	want := []balance.Withdrawal{{
		Order:       "2377225624",
		Sum:         money.Points(75150),
		ProcessedAt: time.Date(2026, time.August, 28, 12, 34, 56, 0, time.UTC),
	}}
	storage := balancemocks.NewMockWithdrawalLister(t)
	storage.EXPECT().ListWithdrawals(ctx, int64(42)).Return(want, nil).Once()

	got, err := balance.NewListService(storage).List(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestListWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage")
	storage := balancemocks.NewMockWithdrawalLister(t)
	ctx := context.Background()
	storage.EXPECT().ListWithdrawals(ctx, int64(42)).Return(nil, storageErr).Once()

	_, err := balance.NewListService(storage).List(ctx, 42)

	require.ErrorIs(t, err, storageErr)
	assert.Contains(t, err.Error(), "list withdrawals")
}

type listContextKey struct{}
