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
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// insertedJobs запоминает поставленные задания вместо очереди.
type insertedJobs struct {
	transactions []*sql.Tx
	args         []river.JobArgs
	err          error
}

func (i *insertedJobs) InsertTx(
	_ context.Context,
	tx *sql.Tx,
	args river.JobArgs,
	_ *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	i.transactions = append(i.transactions, tx)
	i.args = append(i.args, args)

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
		dispatcher := NewDispatcher(queue)

		return dispatcher.Push(context.Background(), tx, "12345678903")
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	want := CheckAccrualArgs{Number: "12345678903"}
	assert.Equal(t, []river.JobArgs{want}, queue.args)
	require.Len(t, queue.transactions, 1)
	assert.Same(t, orderTx, queue.transactions[0], "задание уходит транзакцией заказа")
}

func TestDispatcher_RefusesOutsideTransaction(t *testing.T) {
	gormDB, _ := newMockDB(t)
	queue := &insertedJobs{}

	err := NewDispatcher(queue).Push(context.Background(), gormDB, "12345678903")

	require.ErrorIs(t, err, errOutsideTransaction)
	assert.Empty(t, queue.args)
}

func TestDispatcher_KeepsQueueError(t *testing.T) {
	queueErr := errors.New("queue")
	gormDB, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := gormDB.Transaction(func(tx *gorm.DB) error {
		dispatcher := NewDispatcher(&insertedJobs{err: queueErr})

		return dispatcher.Push(context.Background(), tx, "12345678903")
	})

	require.ErrorIs(t, err, queueErr)
	assert.ErrorContains(t, err, "dispatch check_accrual")
	require.NoError(t, mock.ExpectationsWereMet())
}
