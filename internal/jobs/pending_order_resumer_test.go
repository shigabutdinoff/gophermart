package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/order/mocks"
)

func TestPendingOrderResumer_ForwardsUnfinishedOrdersInOriginalOrder(t *testing.T) {
	ctx := context.Background()
	numbers := []string{"12345678903", "79927398713", "2377225624"}
	orders := ordermocks.NewMockPendingLister(t)
	orders.EXPECT().ListUnfinished(ctx).Return(numbers, nil).Once()
	queue := jobsmocks.NewMockInserter(t)
	first := queue.EXPECT().Insert(
		ctx,
		CheckAccrualArgs{Number: numbers[0]},
		expectedInsertOptions(),
	).Return(&rivertype.JobInsertResult{}, nil).Once()
	second := queue.EXPECT().Insert(
		ctx,
		CheckAccrualArgs{Number: numbers[1]},
		expectedInsertOptions(),
	).Return(&rivertype.JobInsertResult{UniqueSkippedAsDuplicate: true}, nil).Once()
	third := queue.EXPECT().Insert(
		ctx,
		CheckAccrualArgs{Number: numbers[2]},
		expectedInsertOptions(),
	).Return(&rivertype.JobInsertResult{}, nil).Once()
	mock.InOrder(first, second, third)

	inserted, err := NewPendingOrderResumer(
		orders,
		NewDispatcher(queue, "orders"),
	).Resume(ctx)

	require.NoError(t, err)
	assert.Equal(t, 2, inserted)
}

func TestPendingOrderResumer_ReturnsListingErrorBeforeDispatch(t *testing.T) {
	listErr := errors.New("list unfinished orders")
	orders := ordermocks.NewMockPendingLister(t)
	orders.EXPECT().ListUnfinished(mock.Anything).Return(nil, listErr).Once()
	queue := jobsmocks.NewMockInserter(t)

	inserted, err := NewPendingOrderResumer(
		orders,
		NewDispatcher(queue, "orders"),
	).Resume(context.Background())

	assert.Zero(t, inserted)
	require.ErrorIs(t, err, listErr)
}
