package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/accrual"
	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/order"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/order/mocks"
)

func TestCheckAccrual_WritesFinalOutcomeAndFinishesJob(t *testing.T) {
	accrued := money.Points(50050)
	tests := []struct {
		name    string
		info    accrual.OrderInfo
		accrual *money.Points
	}{
		{
			name:    "расчёт завершён",
			info:    accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessed, Accrual: &accrued},
			accrual: &accrued,
		},
		{
			name: "заказ отвергнут",
			info: accrual.OrderInfo{Number: "12345678903", Status: order.StatusInvalid},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			orders := newResultWriter(t, test.info.Status, test.accrual, true, nil)
			client := newAccrualClient(t, test.info, nil)

			err := workCheckAccrual(t, client, orders, testOptions())

			require.NoError(t, err)
		})
	}
}

func TestCheckAccrual_ReturnsUnfinishedOrderToQueue(t *testing.T) {
	info := accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessing}
	orders := newResultWriter(t, order.StatusProcessing, nil, false, nil)
	client := newAccrualClient(t, info, nil)

	options := testOptions()
	options.PollInterval = 3 * time.Second

	err := workCheckAccrual(t, client, orders, options)

	requireSnooze(t, err, 3*time.Second)
}

// Заказ, закрытый другой попыткой, репозиторий отдаёт завершённым.
func TestCheckAccrual_CompletesWhenStoredOrderIsAlreadyFinal(t *testing.T) {
	info := accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessing}
	orders := newResultWriter(t, order.StatusProcessing, nil, true, nil)
	client := newAccrualClient(t, info, nil)

	err := workCheckAccrual(t, client, orders, testOptions())

	require.NoError(t, err, "заказ довёл до конца другой воркер")
}

func TestCheckAccrual_ReturnsCanceledContextWhenResultWriteIsCanceled(t *testing.T) {
	for _, test := range canceledWriteErrors() {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			info := accrual.OrderInfo{
				Number: "12345678903",
				Status: order.StatusProcessed,
			}
			client := jobsmocks.NewMockAccrualClient(t)
			client.EXPECT().OrderInfo(ctx, "12345678903").Return(info, nil).Once()
			orders := ordermocks.NewMockResultWriter(t)
			orders.EXPECT().ApplyResult(
				ctx,
				"12345678903",
				order.StatusProcessed,
				(*money.Points)(nil),
			).
				Run(func(context.Context, string, order.Status, *money.Points) { cancel() }).
				Return(false, test.err).
				Once()
			worker := NewCheckAccrual(zap.NewNop(), client, orders, testOptions())

			err := worker.Work(ctx, newCheckAccrualJob())

			require.ErrorIs(t, err, context.Canceled)
			assert.NotErrorIs(t, err, &river.JobSnoozeError{})
			assert.NotErrorIs(t, err, &river.JobCancelError{})
		})
	}
}

// Недоступное хранилище попыток не тратит: иначе заказ терял бы расчёт из-за
// нашего же сбоя, хотя система расчёта ему не отказывала.
func TestCheckAccrual_SnoozesStorageFailure(t *testing.T) {
	storageErr := errors.New("storage")
	info := accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessed}
	orders := newResultWriter(t, order.StatusProcessed, nil, false, storageErr)
	client := newAccrualClient(t, info, nil)
	core, logs := observer.New(zap.ErrorLevel)
	worker := NewCheckAccrual(zap.New(core), client, orders, testOptions())

	err := worker.Work(context.Background(), newCheckAccrualJob())

	requireSnooze(t, err, testPollInterval)
	assert.NotErrorIs(t, err, &river.JobCancelError{})
	entries := logs.FilterMessage("Не удалось записать исход расчёта").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "12345678903", entries[0].ContextMap()["order"])
}

func TestCheckAccrual_WritesResultByOwnOrderNumber(t *testing.T) {
	info := accrual.OrderInfo{
		Number: "9278923470",
		Status: order.StatusProcessing,
	}
	orders := newResultWriter(t, order.StatusProcessing, nil, false, nil)
	client := newAccrualClient(t, info, nil)

	err := workCheckAccrual(t, client, orders, testOptions())

	requireSnooze(t, err, testPollInterval)
}

func TestCheckAccrual_CancelsJobWhenOrderIsGone(t *testing.T) {
	info := accrual.OrderInfo{
		Number: "12345678903",
		Status: order.StatusProcessing,
	}
	orders := newResultWriter(
		t,
		order.StatusProcessing,
		nil,
		false,
		fmt.Errorf("apply order result: %w", order.ErrNotFound),
	)
	client := newAccrualClient(t, info, nil)

	err := workCheckAccrual(t, client, orders, testOptions())

	require.ErrorIs(t, err, &river.JobCancelError{})
	require.ErrorIs(t, err, order.ErrNotFound)
}
