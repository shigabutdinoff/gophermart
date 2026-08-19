package user

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_MissingCurrentDatabaseIsControlled(t *testing.T) {
	repository := New(nil)

	_, createErr := repository.Create(context.Background(), "user", "hash")
	_, findErr := repository.FindByLogin(context.Background(), "user")

	require.ErrorIs(t, createErr, database.ErrUnavailable)
	require.ErrorIs(t, findErr, database.ErrUnavailable)
}

func TestRepository_CreateUsesCurrentDatabaseAndContext(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	var operationContext context.Context
	var destination *userRow
	require.NoError(t, gormDB.Callback().Create().Before("gorm:create").Register(
		"test:observe-create",
		func(tx *gorm.DB) {
			operationContext = tx.Statement.Context
			destination, _ = tx.Statement.Dest.(*userRow)
		},
	))

	created, err := New(gormDB).Create(ctx, "normalized", "password-hash")

	require.NoError(t, err)
	assert.Same(t, ctx, operationContext)
	require.NotNil(t, destination)
	assert.Equal(t, "normalized", destination.Login)
	assert.Equal(t, "password-hash", destination.PasswordHash)
	assert.Equal(t, "normalized", created.Login)
	assert.Equal(t, "password-hash", created.PasswordHash)
}

func TestRepository_FindByLoginUsesCurrentDatabaseAndContext(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	var operationContext context.Context
	var query string
	var variables []any
	var destination *userRow
	require.NoError(t, gormDB.Callback().Query().After("gorm:query").Register(
		"test:observe-query",
		func(tx *gorm.DB) {
			operationContext = tx.Statement.Context
			query = tx.Statement.SQL.String()
			variables = slices.Clone(tx.Statement.Vars)
			destination, _ = tx.Statement.Dest.(*userRow)
			tx.AddError(gorm.ErrRecordNotFound)
		},
	))

	_, err := New(gormDB).FindByLogin(ctx, "normalized")

	require.ErrorIs(t, err, auth.ErrUserNotFound)
	assert.Same(t, ctx, operationContext)
	require.NotNil(t, destination)
	assert.Contains(t, query, `FROM "users"`)
	assert.Contains(t, query, `WHERE login = $1`)
	assert.Equal(t, []any{"normalized", 1}, variables)
}

func TestUserRowDeclaresColumnsOfUsersTable(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	statement := &gorm.Statement{DB: gormDB}
	require.NoError(t, statement.Parse(&userRow{}))

	require.Contains(t, statement.Schema.FieldsByDBName, "id")
	assert.True(t, statement.Schema.FieldsByDBName["id"].PrimaryKey)
	assert.Contains(t, statement.Schema.FieldsByDBName, "login")
	assert.Contains(t, statement.Schema.FieldsByDBName, "password_hash")
	assert.Contains(t, statement.Schema.FieldsByDBName, "created_at")
}

// Нарушение уникального индекса переводит репозиторий, а не служба над ним.
func TestRepository_CreateMapsStorageError(t *testing.T) {
	storageErr := errors.New("storage")
	tests := []struct {
		name       string
		storageErr error
		wantErr    error
	}{
		{"дубль логина", gorm.ErrDuplicatedKey, auth.ErrLoginTaken},
		{
			"обёрнутый дубль логина",
			fmt.Errorf("create user: %w", gorm.ErrDuplicatedKey),
			auth.ErrLoginTaken,
		},
		{"прочая ошибка хранилища", storageErr, storageErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gormDB := testkit.NewDryRunDB(t)
			require.NoError(t, gormDB.Callback().Create().Before("gorm:create").Register(
				"test:create-error",
				func(tx *gorm.DB) { tx.AddError(tt.storageErr) },
			))

			_, err := New(gormDB).Create(context.Background(), "normalized", "password-hash")

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

type repositoryContextKey struct{}
