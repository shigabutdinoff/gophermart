package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
)

// fakeInserter заменяет сгенерённый мок: разбирая аргументы, testify читает
// поля *sql.Tx рефлексией, а их дописывает фоновая горутина database/sql.
type fakeInserter struct {
	ctx    context.Context
	tx     *sql.Tx
	args   river.JobArgs
	opts   *river.InsertOpts
	calls  int
	result *rivertype.JobInsertResult
	err    error
}

func (f *fakeInserter) InsertTx(
	ctx context.Context,
	tx *sql.Tx,
	args river.JobArgs,
	opts *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	f.calls++
	f.ctx = ctx
	f.tx = tx
	f.args = args
	f.opts = opts

	return f.result, f.err
}

func (f *fakeInserter) Insert(
	ctx context.Context,
	args river.JobArgs,
	opts *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	f.calls++
	f.ctx = ctx
	f.args = args
	f.opts = opts

	return f.result, f.err
}

func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	return sqlDB, mock
}

func TestCheckAccrualArgs_NamesJobInQueue(t *testing.T) {
	assert.Equal(t, "check_accrual", CheckAccrualArgs{}.Kind())
}

func expectedInsertOptions() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:      "orders",
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}

func TestDispatcher_PushesJobWithinOrderTransaction(t *testing.T) {
	sqlDB, sqlMock := newMockDB(t)
	sqlMock.ExpectBegin()
	sqlMock.ExpectCommit()
	ctx := t.Context()
	tx, err := sqlDB.BeginTx(ctx, nil)
	require.NoError(t, err)

	queue := &fakeInserter{result: &rivertype.JobInsertResult{}}

	require.NoError(t, NewDispatcher(queue, "orders").Push(ctx, tx, "12345678903"))

	assert.Equal(t, 1, queue.calls)
	assert.Equal(t, ctx, queue.ctx, "задание ставится контекстом загрузки заказа")
	assert.Same(t, tx, queue.tx, "задание ставится транзакцией заказа")
	assert.Equal(t, CheckAccrualArgs{Number: "12345678903"}, queue.args)
	assert.Equal(t, expectedInsertOptions(), queue.opts)
	require.NoError(t, tx.Commit())
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

func TestDispatcher_KeepsQueueError(t *testing.T) {
	queueErr := errors.New("queue")
	sqlDB, sqlMock := newMockDB(t)
	sqlMock.ExpectBegin()
	sqlMock.ExpectRollback()
	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	require.NoError(t, err)

	queue := &fakeInserter{err: queueErr}

	pushErr := NewDispatcher(queue, "orders").Push(ctx, tx, "12345678903")

	require.ErrorIs(t, pushErr, queueErr)
	assert.Equal(t, 1, queue.calls)
	assert.Same(t, tx, queue.tx, "задание ставится транзакцией заказа")
	assert.ErrorContains(t, pushErr, "dispatch check_accrual")
	require.NoError(t, tx.Rollback())
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

func TestDispatcher_ResumeStopsAfterFirstInsertError(t *testing.T) {
	queueErr := errors.New("queue")
	queue := jobsmocks.NewMockInserter(t)
	ctx := context.Background()
	first := queue.EXPECT().Insert(
		ctx,
		CheckAccrualArgs{Number: "12345678903"},
		expectedInsertOptions(),
	).Return(&rivertype.JobInsertResult{}, nil).Once()
	second := queue.EXPECT().Insert(
		ctx,
		CheckAccrualArgs{Number: "12345678904"},
		expectedInsertOptions(),
	).Return(nil, queueErr).Once()
	mock.InOrder(first, second)

	inserted, err := NewDispatcher(queue, "orders").Resume(
		ctx,
		[]string{"12345678903", "12345678904", "12345678905"},
	)

	assert.Equal(t, 1, inserted)
	require.ErrorIs(t, err, queueErr)
	assert.ErrorContains(t, err, "dispatch check_accrual")
}

func TestDispatcher_ResumeDoesNotCallQueueForNoOrders(t *testing.T) {
	queue := jobsmocks.NewMockInserter(t)

	inserted, err := NewDispatcher(queue, "orders").Resume(context.Background(), nil)

	require.NoError(t, err)
	assert.Zero(t, inserted)
}
