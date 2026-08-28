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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
)

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, sqlMock, err := sqlmock.New()
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

	return gormDB, sqlMock
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

func assertAndForgetTransactionCall(
	t *testing.T,
	queue *jobsmocks.MockInserter,
	call *mock.Call,
) {
	t.Helper()
	require.True(t, queue.AssertExpectations(t))
	call.Unset()
}

func TestDispatcher_PushesJobWithinOrderTransaction(t *testing.T) {
	gormDB, sqlMock := newMockDB(t)
	sqlMock.ExpectBegin()
	sqlMock.ExpectCommit()
	queue := jobsmocks.NewMockInserter(t)
	ctx := context.Background()
	var orderTx *sql.Tx

	err := gormDB.Transaction(func(tx *gorm.DB) error {
		orderTx, _ = tx.Statement.ConnPool.(*sql.Tx)
		insertCall := queue.EXPECT().InsertTx(
			ctx,
			orderTx,
			CheckAccrualArgs{Number: "12345678903"},
			expectedInsertOptions(),
		).Return(&rivertype.JobInsertResult{}, nil).Once()
		dispatcher := NewDispatcher(queue, "orders")

		pushErr := dispatcher.Push(ctx, tx, "12345678903")
		assertAndForgetTransactionCall(t, queue, insertCall)

		return pushErr
	})

	require.NoError(t, err)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

func TestDispatcher_RefusesOutsideTransaction(t *testing.T) {
	gormDB, _ := newMockDB(t)
	queue := jobsmocks.NewMockInserter(t)

	err := NewDispatcher(queue, "orders").Push(context.Background(), gormDB, "12345678903")

	require.ErrorIs(t, err, errOutsideTransaction)
}

func TestDispatcher_KeepsQueueError(t *testing.T) {
	queueErr := errors.New("queue")
	gormDB, sqlMock := newMockDB(t)
	sqlMock.ExpectBegin()
	sqlMock.ExpectRollback()

	err := gormDB.Transaction(func(tx *gorm.DB) error {
		queue := jobsmocks.NewMockInserter(t)
		ctx := context.Background()
		orderTx, _ := tx.Statement.ConnPool.(*sql.Tx)
		insertCall := queue.EXPECT().InsertTx(
			ctx,
			orderTx,
			CheckAccrualArgs{Number: "12345678903"},
			expectedInsertOptions(),
		).Return(nil, queueErr).Once()
		dispatcher := NewDispatcher(queue, "orders")

		pushErr := dispatcher.Push(ctx, tx, "12345678903")
		assertAndForgetTransactionCall(t, queue, insertCall)

		return pushErr
	})

	require.ErrorIs(t, err, queueErr)
	assert.ErrorContains(t, err, "dispatch check_accrual")
	require.NoError(t, sqlMock.ExpectationsWereMet())
}
