package jobs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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

func TestCheckAccrual_ReturnsJobWhileOrderIsNotRegistered(t *testing.T) {
	orders := ordermocks.NewMockResultWriter(t)
	client := newAccrualClient(t, accrual.OrderInfo{}, accrual.ErrNotRegistered)

	err := workCheckAccrual(t, client, orders, testOptions())

	requireSnooze(t, err, testPollInterval)
}

func TestCheckAccrual_CancelsJobOnPermanentAnswer(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "ответ не разобрать", err: accrual.ErrMalformedResponse},
		{name: "неизвестный статус", err: accrual.ErrUnknownStatus},
		{name: "запрос отвергнут", err: &accrual.UnexpectedStatusError{StatusCode: http.StatusBadRequest}},
		{name: "последний код клиента", err: &accrual.UnexpectedStatusError{StatusCode: 499}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			orders := newResultWriter(t, order.StatusInvalid, nil, true, nil)
			client := newAccrualClient(t, accrual.OrderInfo{}, test.err)

			err := workCheckAccrual(t, client, orders, testOptions())

			require.ErrorIs(t, err, &river.JobCancelError{})
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestCheckAccrual_DoesNotTreatNon4xxAsPermanent(t *testing.T) {
	tests := []int{http.StatusContinue, http.StatusFound}

	for _, status := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			clientErr := &accrual.UnexpectedStatusError{StatusCode: status}
			orders := ordermocks.NewMockResultWriter(t)
			client := newAccrualClient(t, accrual.OrderInfo{}, clientErr)

			err := workCheckAccrual(t, client, orders, testOptions())

			requireSnooze(t, err, testPollInterval)
		})
	}
}

func TestCheckAccrual_GiveUpWriteFailureKeepsJobForRetry(t *testing.T) {
	storageErr := errors.New("storage")
	orders := newResultWriter(t, order.StatusInvalid, nil, false, storageErr)
	client := newAccrualClient(t, accrual.OrderInfo{}, accrual.ErrMalformedResponse)
	core, logs := observer.New(zap.ErrorLevel)
	worker := NewCheckAccrual(
		zap.New(core),
		client,
		orders,
		testOptions(),
	)

	err := worker.Work(context.Background(), newCheckAccrualJob())

	requireSnooze(t, err, testPollInterval)
	assert.NotErrorIs(t, err, &river.JobCancelError{}, "отменённое задание некому повторить")
	entry := logs.FilterMessage("Не удалось закрыть заказ отказом расчёта").All()
	require.Len(t, entry, 1)
	assert.Equal(t, "12345678903", entry[0].ContextMap()["order"])
	assert.Equal(t, storageErr.Error(), entry[0].ContextMap()["error"])
}

func TestCheckAccrual_ReturnsCanceledContextWhenGiveUpWriteIsCanceled(t *testing.T) {
	for _, test := range canceledWriteErrors() {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			orders := ordermocks.NewMockResultWriter(t)
			orders.EXPECT().ApplyResult(
				ctx,
				"12345678903",
				order.StatusInvalid,
				(*money.Points)(nil),
			).
				Run(func(context.Context, string, order.Status, *money.Points) { cancel() }).
				Return(false, test.err).
				Once()
			client := newAccrualClient(t, accrual.OrderInfo{}, accrual.ErrMalformedResponse)
			worker := NewCheckAccrual(
				zap.NewNop(),
				client,
				orders,
				testOptions(),
			)

			err := worker.Work(ctx, newCheckAccrualJob())

			require.ErrorIs(t, err, context.Canceled)
			assert.NotErrorIs(t, err, &river.JobSnoozeError{})
			assert.NotErrorIs(t, err, &river.JobCancelError{})
		})
	}
}

// Молчаливый снуз прятал бы лежащий расчёт: об отказах должен остаться след.
func TestCheckAccrual_LogsAndSnoozesTemporaryFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "система расчёта сломалась", err: &accrual.UnexpectedStatusError{StatusCode: http.StatusBadGateway}},
		{name: "сеть недоступна", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}},
		{name: "таймаут", err: &net.DNSError{IsTimeout: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			client := newAccrualClient(t, accrual.OrderInfo{}, test.err)
			orders := ordermocks.NewMockResultWriter(t)
			worker := NewCheckAccrual(
				zap.New(core),
				client,
				orders,
				testOptions(),
			)

			err := worker.Work(context.Background(), newCheckAccrualJob())

			requireSnooze(t, err, testPollInterval)
			entries := logs.FilterMessage("Опрос системы расчёта не удался").All()
			require.Len(t, entries, 1)
			assert.Equal(t, "12345678903", entries[0].ContextMap()["order"])
		})
	}
}

func TestCheckAccrual_CanceledWorkContextIsNotSnoozed(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	client := jobsmocks.NewMockAccrualClient(t)
	client.EXPECT().OrderInfo(ctx, "12345678903").
		Return(accrual.OrderInfo{}, context.DeadlineExceeded).
		Once()
	orders := ordermocks.NewMockResultWriter(t)
	worker := NewCheckAccrual(
		zap.NewNop(),
		client,
		orders,
		testOptions(),
	)

	err := worker.Work(ctx, newCheckAccrualJob())

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, &river.JobSnoozeError{})
}

// Неопознанный отказ расчёта заказу не приговор: попытки он тоже не тратит.
func TestCheckAccrual_SnoozesUnexpectedUntypedError(t *testing.T) {
	orders := ordermocks.NewMockResultWriter(t)
	client := newAccrualClient(t, accrual.OrderInfo{}, errors.New("unexpected"))

	err := workCheckAccrual(t, client, orders, testOptions())

	requireSnooze(t, err, testPollInterval)
	assert.NotErrorIs(t, err, &river.JobCancelError{})
}

func TestCheckAccrual_GiveUpCancelsJobWhenOrderIsGone(t *testing.T) {
	orders := newResultWriter(
		t,
		order.StatusInvalid,
		nil,
		false,
		fmt.Errorf("apply order result: %w", order.ErrNotFound),
	)
	client := newAccrualClient(t, accrual.OrderInfo{}, accrual.ErrMalformedResponse)

	err := workCheckAccrual(
		t,
		client,
		orders,
		testOptions(),
	)

	require.ErrorIs(t, err, &river.JobCancelError{})
	require.ErrorIs(t, err, accrual.ErrMalformedResponse)
}
