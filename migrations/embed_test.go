package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFSContainsUsersMigration(t *testing.T) {
	migration, err := FS.ReadFile("00001_create_users.sql")
	require.NoError(t, err)

	sql := string(migration)
	assert.Contains(t, sql, "-- +goose Up")
	assert.Contains(t, sql, "-- +goose Down")
	assert.Contains(t, sql, "CREATE TABLE users")
	assert.Contains(t, sql, "CONSTRAINT users_login_key UNIQUE (login)")
	assert.Contains(t, sql, "password_hash TEXT NOT NULL")
	assert.Contains(t, sql, "created_at TIMESTAMPTZ NOT NULL")
	assert.Contains(t, sql, "DROP TABLE IF EXISTS users;")
}

func TestFSHasNoLegacyMigrationFiles(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.Equal(t, []string{"00001_create_users.sql"}, names)
}
