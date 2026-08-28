package order_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/order"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/order/mocks"
	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

func TestUploadStoresNormalizedNumber(t *testing.T) {
	storage := ordermocks.NewMockCreator(t)
	ctx := context.Background()
	storage.EXPECT().CreateOrFindOwner(ctx, "12345678903", int64(42)).
		Return(order.Created, nil).
		Once()

	err := order.NewUploadService(storage).Upload(ctx, " 12345678903\n", 42)

	require.NoError(t, err)
}

func TestUploadRejectsNumberBeforeStorage(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   error
	}{
		{name: "пустое тело", number: "  \n", want: ordernumber.ErrEmpty},
		{name: "не проходит Луна", number: "12345678902", want: ordernumber.ErrInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := ordermocks.NewMockCreator(t)

			err := order.NewUploadService(storage).Upload(context.Background(), test.number, 1)

			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestUploadMapsCreateOutcome(t *testing.T) {
	tests := []struct {
		name    string
		outcome order.CreateOutcome
		want    error
	}{
		{name: "создан", outcome: order.Created},
		{name: "уже принадлежит пользователю", outcome: order.AlreadyOwned, want: order.ErrAlreadyUploaded},
		{name: "принадлежит другому пользователю", outcome: order.OwnedByAnother, want: order.ErrOwnedByAnother},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := ordermocks.NewMockCreator(t)
			ctx := context.Background()
			storage.EXPECT().CreateOrFindOwner(ctx, "12345678903", int64(42)).
				Return(test.outcome, nil).
				Once()

			err := order.NewUploadService(storage).Upload(ctx, "12345678903", 42)

			if test.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.want)
			}
		})
	}
}

func TestUploadRejectsUnknownCreateOutcome(t *testing.T) {
	handlerSentinels := []error{
		ordernumber.ErrEmpty,
		ordernumber.ErrInvalid,
		order.ErrAlreadyUploaded,
		order.ErrOwnedByAnother,
	}
	tests := []struct {
		name    string
		outcome order.CreateOutcome
	}{
		{name: "нулевой", outcome: order.CreateOutcome(0)},
		{name: "неизвестный ненулевой", outcome: order.CreateOutcome(255)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := ordermocks.NewMockCreator(t)
			ctx := context.Background()
			storage.EXPECT().CreateOrFindOwner(ctx, "12345678903", int64(42)).
				Return(test.outcome, nil).
				Once()

			err := order.NewUploadService(storage).Upload(ctx, "12345678903", 42)

			assert.EqualError(t, err, fmt.Sprintf("create order: unknown outcome %d", test.outcome))
			for _, sentinel := range handlerSentinels {
				assert.NotErrorIs(t, err, sentinel)
			}
		})
	}
}

func TestUploadWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")
	storage := ordermocks.NewMockCreator(t)
	ctx := context.Background()
	storage.EXPECT().CreateOrFindOwner(ctx, "12345678903", int64(1)).
		Return(order.CreateOutcome(0), storageErr).
		Once()

	err := order.NewUploadService(storage).Upload(ctx, "12345678903", 1)

	require.ErrorIs(t, err, storageErr)
}
