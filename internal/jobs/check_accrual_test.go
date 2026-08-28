package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/shigabutdinoff/gophermart/internal/accrual"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

// insertedJobs запоминает поставленные задания вместо очереди.
type insertedJobs struct {
	transactions []*sql.Tx
	args         []river.JobArgs
	options      []*river.InsertOpts
	err          error
}

func (i *insertedJobs) InsertTx(
	_ context.Context,
	tx *sql.Tx,
	args river.JobArgs,
	options *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	i.transactions = append(i.transactions, tx)
	i.args = append(i.args, args)
	i.options = append(i.options, options)

	return &rivertype.JobInsertResult{}, i.err
}

// newMockDB даёт gorm поверх двойника драйвера: транзакция здесь настоящая.
func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	gormDB, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB, PreferSimpleProtocol: true}),
		&gorm.Config{
			DisableAutomaticPing: true,
			Logger:               gormlogger.Default.LogMode(gormlogger.Silent),
		},
	)
	require.NoError(t, err)

	return gormDB, mock
}

func TestCheckAccrualArgs_NamesJobInQueue(t *testing.T) {
	assert.Equal(t, "check_accrual", CheckAccrualArgs{}.Kind())
}

func TestDispatcher_PushesJobWithinOrderTransaction(t *testing.T) {
	gormDB, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectCommit()
	queue := &insertedJobs{}
	var orderTx *sql.Tx

	err := gormDB.Transaction(func(tx *gorm.DB) error {
		orderTx, _ = tx.Statement.ConnPool.(*sql.Tx)
		dispatcher := NewDispatcher(queue, "orders")

		return dispatcher.Push(context.Background(), tx, "12345678903")
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	want := CheckAccrualArgs{Number: "12345678903"}
	assert.Equal(t, []river.JobArgs{want}, queue.args)
	require.Len(t, queue.transactions, 1)
	assert.Same(t, orderTx, queue.transactions[0], "задание уходит транзакцией заказа")
	require.Len(t, queue.options, 1)
	assert.Equal(t, "orders", queue.options[0].Queue, "задание ждут в названной очереди")
	assert.Equal(t, river.UniqueOpts{ByArgs: true}, queue.options[0].UniqueOpts)
}

func TestDispatcher_RefusesOutsideTransaction(t *testing.T) {
	gormDB, _ := newMockDB(t)
	queue := &insertedJobs{}

	err := NewDispatcher(queue, "orders").Push(context.Background(), gormDB, "12345678903")

	require.ErrorIs(t, err, errOutsideTransaction)
	assert.Empty(t, queue.args)
}

func TestDispatcher_KeepsQueueError(t *testing.T) {
	queueErr := errors.New("queue")
	gormDB, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := gormDB.Transaction(func(tx *gorm.DB) error {
		dispatcher := NewDispatcher(&insertedJobs{err: queueErr}, "orders")

		return dispatcher.Push(context.Background(), tx, "12345678903")
	})

	require.ErrorIs(t, err, queueErr)
	assert.ErrorContains(t, err, "dispatch check_accrual")
	require.NoError(t, mock.ExpectationsWereMet())
}

// fakeAccrual отвечает заготовленным исходом вместо внешней системы.
type fakeAccrual struct {
	info    accrual.OrderInfo
	err     error
	numbers []string
}

func (c *fakeAccrual) OrderInfo(_ context.Context, number string) (accrual.OrderInfo, error) {
	c.numbers = append(c.numbers, number)

	return c.info, c.err
}

// appliedResult запоминает записи исхода вместо хранилища заказов.
type appliedResult struct {
	numbers  []string
	statuses []order.Status
	accruals []*money.Points
	finished bool
	err      error
	onApply  func()
}

func (a *appliedResult) ApplyResult(
	_ context.Context,
	number string,
	status order.Status,
	accrued *money.Points,
) (bool, error) {
	a.numbers = append(a.numbers, number)
	a.statuses = append(a.statuses, status)
	a.accruals = append(a.accruals, accrued)
	if a.onApply != nil {
		a.onApply()
	}

	return a.finished, a.err
}

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
	client *fakeAccrual,
	orders *appliedResult,
	options CheckAccrualOptions,
) error {
	t.Helper()
	worker := NewCheckAccrual(zap.NewNop(), client, orders, options)

	return worker.Work(context.Background(), newCheckAccrualJob())
}

// requireSnooze ждёт возврата задания к опросу без траты попытки.
func requireSnooze(t *testing.T, err error, pause time.Duration) {
	t.Helper()
	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	assert.Equal(t, pause, snooze.Duration)
}

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
			orders := &appliedResult{finished: true}

			err := workCheckAccrual(t, &fakeAccrual{info: test.info}, orders, testOptions())

			require.NoError(t, err)
			assert.Equal(t, []string{"12345678903"}, orders.numbers)
			assert.Equal(t, []order.Status{test.info.Status}, orders.statuses)
			assert.Equal(t, []*money.Points{test.accrual}, orders.accruals)
		})
	}
}

func TestCheckAccrual_ReturnsUnfinishedOrderToQueue(t *testing.T) {
	orders := &appliedResult{}
	client := &fakeAccrual{info: accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessing}}

	options := testOptions()
	options.PollInterval = 3 * time.Second

	err := workCheckAccrual(t, client, orders, options)

	requireSnooze(t, err, 3*time.Second)
	assert.Equal(t, []order.Status{order.StatusProcessing}, orders.statuses)
}

// Заказ, закрытый другой попыткой, репозиторий отдаёт завершённым.
func TestCheckAccrual_CompletesWhenStoredOrderIsAlreadyFinal(t *testing.T) {
	orders := &appliedResult{finished: true}
	client := &fakeAccrual{info: accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessing}}

	err := workCheckAccrual(t, client, orders, testOptions())

	require.NoError(t, err, "заказ довёл до конца другой воркер")
}

func TestCheckAccrual_ReturnsJobWhileOrderIsNotRegistered(t *testing.T) {
	orders := &appliedResult{}

	err := workCheckAccrual(t, &fakeAccrual{err: accrual.ErrNotRegistered}, orders, testOptions())

	requireSnooze(t, err, testPollInterval)
	assert.Empty(t, orders.numbers, "статус заказа неизвестен, трогать его нечем")
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
			orders := &appliedResult{finished: true}

			err := workCheckAccrual(t, &fakeAccrual{err: test.err}, orders, testOptions())

			require.ErrorIs(t, err, &river.JobCancelError{})
			require.ErrorIs(t, err, test.err)
			assert.Equal(t, []order.Status{order.StatusInvalid}, orders.statuses)
			assert.Equal(t, []string{"12345678903"}, orders.numbers)
		})
	}
}

func TestCheckAccrual_DoesNotTreatNon4xxAsPermanent(t *testing.T) {
	tests := []int{http.StatusContinue, http.StatusFound}

	for _, status := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			clientErr := &accrual.UnexpectedStatusError{StatusCode: status}
			orders := &appliedResult{}

			err := workCheckAccrual(t, &fakeAccrual{err: clientErr}, orders, testOptions())

			requireSnooze(t, err, testPollInterval)
			assert.Empty(t, orders.statuses, "расчёт заказу не отказывал")
		})
	}
}

func TestCheckAccrual_GiveUpWriteFailureKeepsJobForRetry(t *testing.T) {
	storageErr := errors.New("storage")
	orders := &appliedResult{err: storageErr}
	core, logs := observer.New(zap.ErrorLevel)
	worker := NewCheckAccrual(
		zap.New(core),
		&fakeAccrual{err: accrual.ErrMalformedResponse},
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
			orders := &appliedResult{err: test.err, onApply: cancel}
			worker := NewCheckAccrual(
				zap.NewNop(),
				&fakeAccrual{err: accrual.ErrMalformedResponse},
				orders,
				testOptions(),
			)

			err := worker.Work(ctx, newCheckAccrualJob())

			require.ErrorIs(t, err, context.Canceled)
			assert.NotErrorIs(t, err, &river.JobSnoozeError{})
			assert.NotErrorIs(t, err, &river.JobCancelError{})
			assert.Equal(t, []order.Status{order.StatusInvalid}, orders.statuses)
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
			worker := NewCheckAccrual(
				zap.New(core),
				&fakeAccrual{err: test.err},
				&appliedResult{},
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
	worker := NewCheckAccrual(
		zap.NewNop(),
		&fakeAccrual{err: context.DeadlineExceeded},
		&appliedResult{},
		testOptions(),
	)

	err := worker.Work(ctx, newCheckAccrualJob())

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, &river.JobSnoozeError{})
}

func TestCheckAccrual_ReturnsCanceledContextWhenResultWriteIsCanceled(t *testing.T) {
	for _, test := range canceledWriteErrors() {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			orders := &appliedResult{err: test.err, onApply: cancel}
			client := &fakeAccrual{info: accrual.OrderInfo{
				Number: "12345678903",
				Status: order.StatusProcessed,
			}}
			worker := NewCheckAccrual(zap.NewNop(), client, orders, testOptions())

			err := worker.Work(ctx, newCheckAccrualJob())

			require.ErrorIs(t, err, context.Canceled)
			assert.NotErrorIs(t, err, &river.JobSnoozeError{})
			assert.NotErrorIs(t, err, &river.JobCancelError{})
			assert.Equal(t, []order.Status{order.StatusProcessed}, orders.statuses)
		})
	}
}

// Неопознанный отказ расчёта заказу не приговор: попытки он тоже не тратит.
func TestCheckAccrual_SnoozesUnexpectedUntypedError(t *testing.T) {
	orders := &appliedResult{}

	err := workCheckAccrual(t, &fakeAccrual{err: errors.New("unexpected")}, orders, testOptions())

	requireSnooze(t, err, testPollInterval)
	assert.NotErrorIs(t, err, &river.JobCancelError{})
	assert.Empty(t, orders.statuses)
}

// Недоступное хранилище попыток не тратит: иначе заказ терял бы расчёт из-за
// нашего же сбоя, хотя система расчёта ему не отказывала.
func TestCheckAccrual_SnoozesStorageFailure(t *testing.T) {
	storageErr := errors.New("storage")
	orders := &appliedResult{err: storageErr}
	client := &fakeAccrual{info: accrual.OrderInfo{Number: "12345678903", Status: order.StatusProcessed}}
	core, logs := observer.New(zap.ErrorLevel)
	worker := NewCheckAccrual(zap.New(core), client, orders, testOptions())

	err := worker.Work(context.Background(), newCheckAccrualJob())

	requireSnooze(t, err, testPollInterval)
	assert.NotErrorIs(t, err, &river.JobCancelError{})
	entries := logs.FilterMessage("Не удалось записать исход расчёта").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "12345678903", entries[0].ContextMap()["order"])
}

func TestCheckAccrual_WaitsOutRefusalByRate(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		pause time.Duration
	}{
		{
			name:  "срок назван системой расчёта",
			err:   &accrual.TooManyRequestsError{RetryAfter: 7 * time.Second},
			pause: 7 * time.Second,
		},
		{
			name:  "срок не назван",
			err:   &accrual.TooManyRequestsError{},
			pause: 4 * time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			orders := &appliedResult{}

			options := testOptions()
			options.ThrottleBackoff = 4 * time.Second

			err := workCheckAccrual(t, &fakeAccrual{err: test.err}, orders, options)

			requireSnooze(t, err, test.pause)
			assert.Empty(t, orders.numbers, "статус заказа неизвестен, трогать его нечем")
		})
	}
}

// heldQueue запоминает, на сколько воркер придержал очередь опроса.
type heldQueue struct {
	pauses []time.Duration
}

func (h *heldQueue) Pause(_ context.Context, pause time.Duration) {
	h.pauses = append(h.pauses, pause)
}

func TestCheckAccrual_HoldsWholeQueueOnRefusalByRate(t *testing.T) {
	held := &heldQueue{}
	options := testOptions()
	options.Throttle = held
	client := &fakeAccrual{err: &accrual.TooManyRequestsError{RetryAfter: 30 * time.Second}}

	err := workCheckAccrual(t, client, &appliedResult{}, options)

	requireSnooze(t, err, 30*time.Second)
	assert.Equal(t, []time.Duration{30 * time.Second}, held.pauses, "отказ касается всех воркеров")
}

func TestCheckAccrual_LeavesQueueAloneOnOtherAnswers(t *testing.T) {
	held := &heldQueue{}
	options := testOptions()
	options.Throttle = held
	client := &fakeAccrual{err: accrual.ErrNotRegistered}

	err := workCheckAccrual(t, client, &appliedResult{}, options)

	requireSnooze(t, err, testPollInterval)
	assert.Empty(t, held.pauses)
}

func TestCheckAccrual_WritesResultByOwnOrderNumber(t *testing.T) {
	orders := &appliedResult{}
	client := &fakeAccrual{info: accrual.OrderInfo{
		Number: "9278923470",
		Status: order.StatusProcessing,
	}}

	err := workCheckAccrual(t, client, orders, testOptions())

	requireSnooze(t, err, testPollInterval)
	assert.Equal(t, []string{"12345678903"}, orders.numbers, "строку заказа выбирает задание, а не ответ")
}

func TestCheckAccrual_CancelsJobWhenOrderIsGone(t *testing.T) {
	orders := &appliedResult{err: fmt.Errorf("apply order result: %w", order.ErrNotFound)}
	client := &fakeAccrual{info: accrual.OrderInfo{
		Number: "12345678903",
		Status: order.StatusProcessing,
	}}

	err := workCheckAccrual(t, client, orders, testOptions())

	require.ErrorIs(t, err, &river.JobCancelError{})
	require.ErrorIs(t, err, order.ErrNotFound)
}

func TestCheckAccrual_GiveUpCancelsJobWhenOrderIsGone(t *testing.T) {
	orders := &appliedResult{err: fmt.Errorf("apply order result: %w", order.ErrNotFound)}

	err := workCheckAccrual(
		t,
		&fakeAccrual{err: accrual.ErrMalformedResponse},
		orders,
		testOptions(),
	)

	require.ErrorIs(t, err, &river.JobCancelError{})
	require.ErrorIs(t, err, accrual.ErrMalformedResponse)
}
