package balance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/money"
)

type fakeWithdrawalLister struct {
	withdrawals []Withdrawal
	err         error
	ctx         context.Context
	userID      int64
}

func (l *fakeWithdrawalLister) ListWithdrawals(
	ctx context.Context,
	userID int64,
) ([]Withdrawal, error) {
	l.ctx = ctx
	l.userID = userID

	return l.withdrawals, l.err
}

func TestListReturnsUserWithdrawalsWithoutChanges(t *testing.T) {
	ctx := context.WithValue(context.Background(), listContextKey{}, "value")
	want := []Withdrawal{{
		Order:       "2377225624",
		Sum:         money.Points(75150),
		ProcessedAt: time.Date(2026, time.August, 28, 12, 34, 56, 0, time.UTC),
	}}
	storage := &fakeWithdrawalLister{withdrawals: want}

	got, err := NewListService(storage).List(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Same(t, ctx, storage.ctx)
	assert.Equal(t, int64(42), storage.userID)
}

func TestListWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage")

	_, err := NewListService(&fakeWithdrawalLister{err: storageErr}).List(context.Background(), 42)

	require.ErrorIs(t, err, storageErr)
	assert.Contains(t, err.Error(), "list withdrawals")
}

type listContextKey struct{}
