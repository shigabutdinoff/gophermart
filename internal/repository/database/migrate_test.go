package database

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schema "github.com/shigabutdinoff/gophermart/migrations"
)

// migrationsDir указывает корень встроенной файловой системы миграций
const migrationsDir = "."

func TestMigrate_StartsDomainMigrationsBeforeQueue(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	domainErr := errors.New("domain migration")
	mock.ExpectQuery(`SELECT EXISTS .*goose_db_version`).WillReturnError(domainErr)
	t.Cleanup(func() { _ = db.Close() })

	err = Migrate(context.Background(), db)

	require.ErrorIs(t, err, domainErr)
	assert.ErrorContains(t, err, "apply migrations")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Состав встроенной ФС проверяет пакет migrations, здесь важен сам каталог.
func TestMigrate_UsesEmbeddedMigrations(t *testing.T) {
	entries, err := schema.FS.ReadDir(migrationsDir)
	require.NoError(t, err)

	assert.NotEmpty(t, entries)
}

func TestNewMigrationProviderUsesEmbeddedMigrations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	t.Cleanup(func() { _ = db.Close() })

	provider, err := newMigrationProvider(db)
	require.NoError(t, err)

	assert.NotEmpty(t, provider.ListSources())
}
