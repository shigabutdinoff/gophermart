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
)

func TestRepository_MissingCurrentDatabaseIsControlled(t *testing.T) {
	repository := New(nil)

	_, createErr := repository.CreateOrFindOwner(context.Background(), "12345678903", 1)

	require.ErrorIs(t, createErr, database.ErrUnavailable)
}

func TestRepository_CreateOrFindOwnerStoresNewOrderWithContext(t *testing.T) {
	gormDB := newDryRunDB(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	var operationContext context.Context
	var destination *orderRow
	var query string
	require.NoError(t, gormDB.Callback().Create().After("gorm:create").Register(
		"test:observe-create",
		func(tx *gorm.DB) {
			operationContext = tx.Statement.Context
			destination, _ = tx.Statement.Dest.(*orderRow)
			query = tx.Statement.SQL.String()
			// вставку без конфликта считает база, DryRun строку не пишет
			tx.RowsAffected = 1
		},
	))

	outcome, err := createOrFindOwner(gormDB.WithContext(ctx), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Created, outcome)
	assert.Same(t, ctx, operationContext)
	require.NotNil(t, destination)
	assert.Equal(t, "12345678903", destination.Number)
	assert.Equal(t, int64(42), destination.UserID)
	assert.Equal(t, domain.StatusNew, destination.Status)
	assert.False(t, destination.UploadedAt.IsZero())
	assert.Contains(t, query, `ON CONFLICT ("number") DO NOTHING`)
}

func TestRepository_CreateOrFindOwnerClassifiesTakenNumber(t *testing.T) {
	tests := []struct {
		name         string
		storedUserID int64
		want         domain.CreateOutcome
	}{
		{name: "тот же пользователь", storedUserID: 42, want: domain.AlreadyOwned},
		{name: "другой пользователь", storedUserID: 7, want: domain.OwnedByAnother},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gormDB := newDryRunDB(t)
			var query string
			var variables []any
			require.NoError(t, gormDB.Callback().Query().After("gorm:query").Register(
				"test:observe-query",
				func(tx *gorm.DB) {
					query = tx.Statement.SQL.String()
					variables = slices.Clone(tx.Statement.Vars)
					if row, ok := tx.Statement.Dest.(*orderRow); ok {
						row.Number = "12345678903"
						row.UserID = test.storedUserID
					}
				},
			))

			outcome, err := createOrFindOwner(gormDB, "12345678903", 42)

			require.NoError(t, err)
			assert.Equal(t, test.want, outcome)
			assert.Contains(t, query, `SELECT "user_id"`)
			assert.Contains(t, query, `FROM "orders"`)
			assert.Contains(t, query, `WHERE number = $1`)
			assert.Equal(t, []any{"12345678903", 1}, variables)
		})
	}
}

func TestRepository_CreateOrFindOwnerKeepsStorageError(t *testing.T) {
	storageErr := errors.New("storage")
	tests := []struct {
		name     string
		register func(*gorm.DB) error
	}{
		{
			name: "отказ вставки",
			register: func(db *gorm.DB) error {
				return db.Callback().Create().After("gorm:create").Register(
					"test:create-error",
					func(tx *gorm.DB) { tx.AddError(storageErr) },
				)
			},
		},
		{
			name: "отказ поиска владельца",
			register: func(db *gorm.DB) error {
				return db.Callback().Query().After("gorm:query").Register(
					"test:query-error",
					func(tx *gorm.DB) { tx.AddError(storageErr) },
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gormDB := newDryRunDB(t)
			require.NoError(t, test.register(gormDB))

			_, err := createOrFindOwner(gormDB, "12345678903", 1)

			assert.ErrorIs(t, err, storageErr)
		})
	}
}

func TestRepository_CreateOrFindOwnerKeepsMissingRowAsStorageError(t *testing.T) {
	gormDB := newDryRunDB(t)
	require.NoError(t, gormDB.Callback().Query().After("gorm:query").Register(
		"test:query-missing-row",
		func(tx *gorm.DB) { tx.AddError(gorm.ErrRecordNotFound) },
	))

	_, err := createOrFindOwner(gormDB, "12345678903", 1)

	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOrderRowDeclaresColumnsOfOrdersTable(t *testing.T) {
	gormDB := newDryRunDB(t)
	statement := &gorm.Statement{DB: gormDB}
	require.NoError(t, statement.Parse(&orderRow{}))

	require.Contains(t, statement.Schema.FieldsByDBName, "id")
	assert.True(t, statement.Schema.FieldsByDBName["id"].PrimaryKey)
	assert.Contains(t, statement.Schema.FieldsByDBName, "number")
	assert.NotContains(t, statement.Schema.FieldsByDBName, "number_hash")
	assert.Contains(t, statement.Schema.FieldsByDBName, "user_id")
	assert.Contains(t, statement.Schema.FieldsByDBName, "status")
	assert.Contains(t, statement.Schema.FieldsByDBName, "uploaded_at")
}

type repositoryContextKey struct{}

func newDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()

	gormDB, err := database.Connection(
		"postgres://gophermart:password@127.0.0.1:1/gophermart?sslmode=disable",
	)
	require.NoError(t, err)
	gormDB = gormDB.Session(&gorm.Session{
		DryRun:                 true,
		SkipDefaultTransaction: true,
	})
	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	return gormDB
}
