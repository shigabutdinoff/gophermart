package order

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_MissingCurrentDatabaseIsControlled(t *testing.T) {
	repository := newRepositoryWithPusher(t, database.Session{}, (&testkit.RecordingPusher{}).ExpectPushes(t, 0))

	_, createErr := repository.CreateOrFindOwner(context.Background(), "12345678903", 1)
	_, listErr := repository.ListByUser(context.Background(), 1)

	require.ErrorIs(t, createErr, database.ErrUnavailable)
	require.ErrorIs(t, listErr, database.ErrUnavailable)
}

func TestRepository_CreateOrFindOwnerCreatesNewOrder(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	jobs := &testkit.RecordingPusher{}
	number := "12345678903"
	digest := sha256.Sum256([]byte(number))
	observed := testkit.ObserveStatements(t, gormDB)

	outcome, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 1)).
		CreateOrFindOwner(context.Background(), number, 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Created, outcome)
	statement := observed()
	destination, ok := statement.Dest.(*orderRow)
	require.True(t, ok)
	assert.Equal(t, number, destination.Number)
	assert.Equal(t, digest[:], destination.NumberHash)
	assert.Equal(t, int64(42), destination.UserID)
	assert.Equal(t, domain.StatusNew, destination.Status)
	assert.Nil(t, destination.Accrual)
	assert.Contains(
		t,
		statement.SQL,
		`INSERT INTO "orders" ("number","number_hash","user_id","status","uploaded_at","accrual")`,
	)
	assert.Contains(t, statement.SQL, `ON CONFLICT ("number_hash") DO NOTHING`)
	require.Len(t, statement.Variables, 6)
	assert.Equal(t, number, statement.Variables[0])
	assert.Equal(t, digest[:], statement.Variables[1])
	assert.Equal(t, int64(42), statement.Variables[2])
	assert.Equal(t, domain.StatusNew, statement.Variables[3])
	assert.Nil(t, statement.Variables[5])
	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
	assert.Equal(t, []string{number}, jobs.Numbers)
	require.Len(t, jobs.Transactions, 1)
	assert.NotNil(t, jobs.Transactions[0], "задание ставится транзакцией заказа")
}

func TestRepository_CreateOrFindOwnerRecognizesRepeatBySameOwner(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	jobs := &testkit.RecordingPusher{}
	testkit.SetCreateResult(gormDB, 0, nil)
	testkit.SetQueryResult(gormDB, []orderRow{{Number: "12345678903", UserID: 42}}, nil)

	outcome, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, domain.AlreadyOwned, outcome)
	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
	assert.Empty(t, jobs.Numbers, "повтор заказа задания не заводит")
}

func TestRepository_CreateOrFindOwnerRecognizesAnotherOwner(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	jobs := &testkit.RecordingPusher{}
	testkit.SetCreateResult(gormDB, 0, nil)
	testkit.SetQueryResult(gormDB, []orderRow{{Number: "12345678903", UserID: 7}}, nil)

	outcome, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, domain.OwnedByAnother, outcome)
	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
	assert.Empty(t, jobs.Numbers, "чужой заказ задания не заводит")
}

func TestRepository_CreateOrFindOwnerRejectsDigestCollision(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	jobs := &testkit.RecordingPusher{}
	testkit.SetCreateResult(gormDB, 0, nil)
	testkit.SetQueryResult(gormDB, []orderRow{{Number: "9278923470", UserID: 7}}, nil)

	_, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.ErrorIs(t, err, errNumberHashCollision)
	assert.NotErrorIs(t, err, domain.ErrOwnedByAnother)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerKeepsWriteError(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	jobs := &testkit.RecordingPusher{}
	testkit.SetCreateResult(gormDB, 0, storageErr)

	_, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 1)

	require.ErrorIs(t, err, storageErr)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerRollsBackWhenJobIsNotQueued(t *testing.T) {
	queueErr := errors.New("queue")
	session, gormDB := testkit.NewDryRunSession(t)
	jobs := &testkit.RecordingPusher{Err: queueErr}

	_, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 1)).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.ErrorIs(t, err, queueErr)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerRefusesWithoutPusher(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)

	_, err := New(session).
		CreateOrFindOwner(context.Background(), "12345678903", 42)

	require.ErrorIs(t, err, errPusherMissing)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_CreateOrFindOwnerReportsMissingDatabaseFirst(t *testing.T) {
	_, err := New(database.Session{}).CreateOrFindOwner(context.Background(), "12345678903", 42)

	// без БД очередь и не подключалась, причина отказа именно она
	require.ErrorIs(t, err, database.ErrUnavailable)
	assert.NotErrorIs(t, err, errPusherMissing)
}

func TestRepository_AttachPusherRejectsSecondAttachment(t *testing.T) {
	session, _ := testkit.NewDryRunSession(t)
	repository := New(session)
	first := &testkit.RecordingPusher{}
	second := &testkit.RecordingPusher{}

	require.NoError(t, repository.AttachPusher(first.ExpectPushes(t, 1)))
	err := repository.AttachPusher(second.ExpectPushes(t, 0))

	require.Error(t, err)
	assert.ErrorContains(t, err, "already configured")
	_, err = repository.CreateOrFindOwner(context.Background(), "12345678903", 42)
	require.NoError(t, err)
	assert.Equal(t, []string{"12345678903"}, first.Numbers)
	assert.Empty(t, second.Numbers)
}

func TestRepository_AttachPusherRejectsNilWithoutConsumingAttachment(t *testing.T) {
	repository := New(database.Session{})

	err := repository.AttachPusher(nil)

	require.Error(t, err)
	assert.NotErrorIs(t, err, errPusherMissing)
	assert.ErrorContains(t, err, "nil")
	require.NoError(t, repository.AttachPusher((&testkit.RecordingPusher{}).ExpectPushes(t, 0)))
}

func TestRepository_CreateOrFindOwnerKeepsReadError(t *testing.T) {
	storageErr := errors.New("storage")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "ошибка хранилища", err: storageErr, want: storageErr},
		{
			name: "отсутствующая строка",
			err:  gorm.ErrRecordNotFound,
			want: domain.ErrNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, gormDB := testkit.NewDryRunSession(t)
			jobs := &testkit.RecordingPusher{}
			testkit.SetCreateResult(gormDB, 0, nil)
			testkit.SetQueryResult(gormDB, []orderRow(nil), test.err)

			_, err := newRepositoryWithPusher(t, session, jobs.ExpectPushes(t, 0)).
				CreateOrFindOwner(context.Background(), "12345678903", 1)

			require.ErrorIs(t, err, test.want)
			assert.NotErrorIs(t, err, gorm.ErrRecordNotFound)
			assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
		})
	}
}

// Ту же защиту репозиторий обязан дать и на живом пути создания заказа.
func TestRepository_CreateOrFindOwnerRefusesTransactionOutsideDatabaseSQL(t *testing.T) {
	gormDB := testkit.NewDryRunDBWithoutSQLTransaction(t)
	testkit.SetCreateResult(gormDB, 1, nil)
	jobs := &testkit.RecordingPusher{}

	_, err := newRepositoryWithPusher(t, database.NewSession(gormDB), jobs.ExpectPushes(t, 0)).
		CreateOrFindOwner(context.Background(), "12345678903", 1)

	require.ErrorIs(t, err, database.ErrOutsideTransaction)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}
