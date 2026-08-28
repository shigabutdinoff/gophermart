package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/accrual"
	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/order"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/order/mocks"
)

type canceledWriteError struct {
	name string
	err  error
}

func canceledWriteErrors() []canceledWriteError {
	return []canceledWriteError{
		{name: "ошибка хранилища", err: errors.New("storage")},
		{name: "заказ не найден", err: fmt.Errorf("apply order result: %w", order.ErrNotFound)},
	}
}

func newCheckAccrualJob() *river.Job[CheckAccrualArgs] {
	return &river.Job[CheckAccrualArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1},
		Args:   CheckAccrualArgs{Number: "12345678903"},
	}
}

// Значения, которые воркеру в работе даёт конфигурация очереди.
const (
	testPollInterval    = time.Second
	testThrottleBackoff = 10 * time.Second
)

func testOptions() CheckAccrualOptions {
	return CheckAccrualOptions{
		PollInterval:    testPollInterval,
		ThrottleBackoff: testThrottleBackoff,
	}
}

func workCheckAccrual(
	t *testing.T,
	client AccrualClient,
	orders order.ResultWriter,
	options CheckAccrualOptions,
) error {
	t.Helper()
	worker := NewCheckAccrual(zap.NewNop(), client, orders, options)

	return worker.Work(context.Background(), newCheckAccrualJob())
}

func newAccrualClient(
	t *testing.T,
	info accrual.OrderInfo,
	err error,
) *jobsmocks.MockAccrualClient {
	t.Helper()
	client := jobsmocks.NewMockAccrualClient(t)
	client.EXPECT().OrderInfo(mock.Anything, "12345678903").Return(info, err).Once()

	return client
}

func newResultWriter(
	t *testing.T,
	status order.Status,
	accrued *money.Points,
	finished bool,
	err error,
) *ordermocks.MockResultWriter {
	t.Helper()
	orders := ordermocks.NewMockResultWriter(t)
	orders.EXPECT().ApplyResult(mock.Anything, "12345678903", status, accrued).
		Return(finished, err).
		Once()

	return orders
}

// requireSnooze ждёт возврата задания к опросу без траты попытки.
func requireSnooze(t *testing.T, err error, pause time.Duration) {
	t.Helper()
	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	assert.Equal(t, pause, snooze.Duration)
}
