package user

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_MissingCurrentDatabaseIsControlled(t *testing.T) {
	repository := New(database.Session{})

	_, createErr := repository.Create(context.Background(), "user", "hash")
	_, findErr := repository.FindByLogin(context.Background(), "user")

	require.ErrorIs(t, createErr, database.ErrUnavailable)
	require.ErrorIs(t, findErr, database.ErrUnavailable)
}

func TestRepository_CreateUsesCurrentDatabaseAndContext(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	observed := testkit.ObserveStatements(t, gormDB)

	created, err := New(session).Create(ctx, "normalized", "password-hash")

	require.NoError(t, err)
	statement := observed()
	assert.Same(t, ctx, statement.Context)
	destination, ok := statement.Dest.(*userRow)
	require.True(t, ok)
	assert.Equal(t, "normalized", destination.Login)
	assert.Equal(t, "password-hash", destination.PasswordHash)
	assert.Equal(t, "normalized", created.Login)
	assert.Equal(t, "password-hash", created.PasswordHash)
}

func TestRepository_FindByLoginUsesCurrentDatabaseAndContext(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	observed := testkit.ObserveStatements(t, gormDB)
	testkit.SetQueryResult(gormDB, []userRow(nil), gorm.ErrRecordNotFound)

	_, err := New(session).FindByLogin(ctx, "normalized")

	require.ErrorIs(t, err, auth.ErrUserNotFound)
	statement := observed()
	assert.Same(t, ctx, statement.Context)
	assert.IsType(t, &userRow{}, statement.Dest)
	assert.Contains(t, statement.SQL, `FROM "users"`)
	assert.Contains(t, statement.SQL, `WHERE login = $1`)
	assert.Equal(t, []any{"normalized", 1}, statement.Variables)
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
			session, gormDB := testkit.NewDryRunSession(t)
			testkit.SetCreateResult(gormDB, 0, tt.storageErr)

			_, err := New(session).
				Create(context.Background(), "normalized", "password-hash")

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

type repositoryContextKey struct{}
