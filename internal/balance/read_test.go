package balance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/balance"
	balancemocks "github.com/shigabutdinoff/gophermart/internal/balance/mocks"
	"github.com/shigabutdinoff/gophermart/internal/money"
)

func TestReadReturnsUserBalance(t *testing.T) {
	want := balance.Balance{Current: money.Points(50050), Withdrawn: money.Points(4200)}
	reader := balancemocks.NewMockReader(t)
	ctx := context.Background()
	reader.EXPECT().Balance(ctx, int64(42)).Return(want, nil).Once()

	got, err := balance.NewReadService(reader).Read(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestReadWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")
	reader := balancemocks.NewMockReader(t)
	ctx := context.Background()
	reader.EXPECT().Balance(ctx, int64(42)).Return(balance.Balance{}, storageErr).Once()

	_, err := balance.NewReadService(reader).Read(ctx, 42)

	require.ErrorIs(t, err, storageErr)
	assert.ErrorContains(t, err, "read balance")
}
