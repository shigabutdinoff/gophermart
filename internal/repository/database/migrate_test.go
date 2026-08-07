package database

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schema "github.com/shigabutdinoff/gophermart/migrations"
)

// Двойник без ожиданий отвергает любой запрос, так же ведёт себя мёртвая БД.
func TestMigrate_UnreachableDatabaseReturnsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	t.Cleanup(func() { _ = db.Close() })

	err = Migrate(context.Background(), db)

	require.Error(t, err)
}

// Состав встроенной ФС проверяет пакет migrations, здесь важен сам каталог.
func TestMigrate_UsesEmbeddedMigrations(t *testing.T) {
	entries, err := schema.FS.ReadDir(migrationsDir)
	require.NoError(t, err)

	assert.NotEmpty(t, entries)
}
