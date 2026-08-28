package order_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/order"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/order/mocks"
)

func TestListReturnsOrdersOfUser(t *testing.T) {
	uploaded := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	want := []order.Order{{Number: "12345678903", Status: order.StatusNew, UploadedAt: uploaded}}
	lister := ordermocks.NewMockLister(t)
	ctx := context.Background()
	lister.EXPECT().ListByUser(ctx, int64(42)).Return(want, nil).Once()

	list, err := order.NewListService(lister).List(ctx, 42)

	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "12345678903", list[0].Number)
	assert.Equal(t, order.StatusNew, list[0].Status)
	assert.Equal(t, uploaded, list[0].UploadedAt)
}

func TestListWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")
	lister := ordermocks.NewMockLister(t)
	ctx := context.Background()
	lister.EXPECT().ListByUser(ctx, int64(1)).Return(nil, storageErr).Once()

	_, err := order.NewListService(lister).List(ctx, 1)

	require.ErrorIs(t, err, storageErr)
}
