package order

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_MissingCurrentDatabaseIsControlled(t *testing.T) {
	repository := newRepositoryWithPusher(t, nil, (&pusherState{}).mock(t, 0))

	_, createErr := repository.CreateOrFindOwner(context.Background(), "12345678903", 1)
	_, listErr := repository.ListByUser(context.Background(), 1)

	require.ErrorIs(t, createErr, database.ErrUnavailable)
	require.ErrorIs(t, listErr, database.ErrUnavailable)
}

func TestRepository_CreateOrFindOwnerCreatesNewOrder(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	jobs := &pusherState{}
	number := "12345678903"
	var query string
	var variables []any
	var destination *orderRow
	require.NoError(t, gormDB.Callback().Create().After("testkit:dry-run-create-result").Register(
		"test:observe-order-create",
		func(tx *gorm.DB) {
			query = tx.Statement.SQL.String()
			variables = slices.Clone(tx.Statement.Vars)
			destination, _ = tx.Statement.Dest.(*orderRow)
		},
	))

	outcome, err := newRepositoryWithPusher(t, gormDB, jobs.mock(t, 1)).
		CreateOrFindOwner(context.Background(), number, 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Created, outcome)
	require.NotNil(t, destination)
	assert.Equal(t, number, destination.Number)
	assert.Equal(t, int64(42), destination.UserID)
	assert.Equal(t, domain.StatusNew, destination.Status)
	assert.Nil(t, destination.Accrual)
	assert.Contains(t, query, `INSERT INTO "orders" ("number","user_id","status","uploaded_at","accrual")`)
	assert.Contains(t, query, `ON CONFLICT ("number") DO NOTHING`)
	require.Len(t, variables, 5)
	assert.Equal(t, number, variables[0])
	assert.Equal(t, int64(42), variables[1])
	assert.Equal(t, domain.StatusNew, variables[2])
	assert.Nil(t, variables[4])
	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
	assert.Equal(t, []string{number}, jobs.numbers)
	require.Len(t, jobs.transactions, 1)
	assert.NotNil(t, jobs.transactions[0], "задание ставится транзакцией заказа")
}

func TestRepository_CreateOrFindOwnerRecognizesRepeatBySameOwner(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	jobs := &pusherState{}
	testkit.SetCreateResult(t, gormDB, 0, nil)
	testkit.SetQueryResult(t, gormDB, []orderRow{{Number: "12345678903", UserID: 42}}, nil)

	outcome, err := newRepositoryWithPusher(t, gormDB, jobs.mock(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, domain.AlreadyOwned, outcome)
	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
	assert.Empty(t, jobs.numbers, "повтор заказа задания не заводит")
}

func TestRepository_CreateOrFindOwnerRecognizesAnotherOwner(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	jobs := &pusherState{}
	testkit.SetCreateResult(t, gormDB, 0, nil)
	testkit.SetQueryResult(t, gormDB, []orderRow{{Number: "12345678903", UserID: 7}}, nil)

	outcome, err := newRepositoryWithPusher(t, gormDB, jobs.mock(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, domain.OwnedByAnother, outcome)
	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
	assert.Empty(t, jobs.numbers, "чужой заказ задания не заводит")
}

func TestRepository_CreateOrFindOwnerKeepsWriteError(t *testing.T) {
	storageErr := errors.New("storage")
	gormDB := testkit.NewDryRunDB(t)
	jobs := &pusherState{}
	testkit.SetCreateResult(t, gormDB, 0, storageErr)

	_, err := newRepositoryWithPusher(t, gormDB, jobs.mock(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 1)

	require.ErrorIs(t, err, storageErr)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerRollsBackWhenJobIsNotQueued(t *testing.T) {
	queueErr := errors.New("queue")
	gormDB := testkit.NewDryRunDB(t)
	jobs := &pusherState{err: queueErr}

	_, err := newRepositoryWithPusher(t, gormDB, jobs.mock(t, 1)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.ErrorIs(t, err, queueErr)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerRefusesWithoutPusher(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)

	_, err := New(gormDB).CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.ErrorIs(t, err, errPusherMissing)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerReportsMissingDatabaseFirst(t *testing.T) {
	_, err := New(nil).CreateOrFindOwner(context.Background(), "12345678903", 42)

	// без БД очередь и не подключалась, причина отказа именно она
	require.ErrorIs(t, err, database.ErrUnavailable)
	assert.NotErrorIs(t, err, errPusherMissing)
}

func TestRepository_AttachPusherRejectsSecondAttachment(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	repository := New(gormDB)
	first := &pusherState{}
	second := &pusherState{}

	require.NoError(t, repository.AttachPusher(first.mock(t, 1)))
	err := repository.AttachPusher(second.mock(t, 0))

	require.Error(t, err)
	assert.ErrorContains(t, err, "already configured")
	_, err = repository.CreateOrFindOwner(context.Background(), "12345678903", 42)
	require.NoError(t, err)
	assert.Equal(t, []string{"12345678903"}, first.numbers)
	assert.Empty(t, second.numbers)
}

func TestRepository_AttachPusherRejectsNilWithoutConsumingAttachment(t *testing.T) {
	repository := New(nil)

	err := repository.AttachPusher(nil)

	require.Error(t, err)
	assert.NotErrorIs(t, err, errPusherMissing)
	assert.ErrorContains(t, err, "nil")
	require.NoError(t, repository.AttachPusher((&pusherState{}).mock(t, 0)))
}

func TestRepository_CreateOrFindOwnerKeepsReadError(t *testing.T) {
	storageErr := errors.New("storage")
	tests := []struct {
		name string
		err  error
	}{
		{name: "ошибка хранилища", err: storageErr},
		{name: "отсутствующая строка", err: gorm.ErrRecordNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gormDB := testkit.NewDryRunDB(t)
			jobs := &pusherState{}
			testkit.SetCreateResult(t, gormDB, 0, nil)
			testkit.SetQueryResult(t, gormDB, []orderRow(nil), test.err)

			_, err := newRepositoryWithPusher(t, gormDB, jobs.mock(t, 0)).
				CreateOrFindOwner(context.Background(), "12345678903", 1)

			require.ErrorIs(t, err, test.err)
			assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
		})
	}
}
