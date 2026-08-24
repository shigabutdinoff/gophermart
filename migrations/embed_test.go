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

func TestFSContainsOrdersMigration(t *testing.T) {
	migration, err := FS.ReadFile("00002_create_orders.sql")
	require.NoError(t, err)

	sql := string(migration)
	assert.Contains(t, sql, "-- +goose Up")
	assert.Contains(t, sql, "-- +goose Down")
	assert.Contains(t, sql, "CREATE TABLE orders")
	assert.Contains(t, sql, "number TEXT NOT NULL")
	assert.Contains(t, sql, "CONSTRAINT orders_number_key UNIQUE (number)")
	assert.NotContains(t, sql, "number_hash")
	assert.Contains(t, sql, "user_id BIGINT NOT NULL REFERENCES users (id)")
	assert.Contains(t, sql, "CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED'))")
	assert.Contains(t, sql, "uploaded_at TIMESTAMPTZ NOT NULL")
	assert.NotContains(t, sql, "orders_user_uploaded_idx")
	assert.Contains(t, sql, "DROP TABLE IF EXISTS orders;")
}

func TestFSContainsOrderAccrualMigration(t *testing.T) {
	migration, err := FS.ReadFile("00003_add_order_accrual.sql")
	require.NoError(t, err)

	sql := string(migration)
	assert.Contains(t, sql, "-- +goose Up")
	assert.Contains(t, sql, "-- +goose Down")
	assert.Contains(t, sql, "ALTER TABLE orders ADD COLUMN accrual BIGINT")
	// колонка остаётся nullable и без значения по умолчанию
	assert.NotContains(t, sql, "NOT NULL")
	assert.NotContains(t, sql, "DEFAULT")
	assert.Contains(t, sql, "CONSTRAINT orders_accrual_non_negative CHECK (accrual >= 0);")
	assert.Contains(t, sql, "COMMENT ON COLUMN orders.accrual IS")
	assert.Contains(t, sql, "DROP COLUMN IF EXISTS accrual;")
}

func TestFSHasNoLegacyMigrationFiles(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.Equal(t, []string{
		"00001_create_users.sql",
		"00002_create_orders.sql",
		"00003_add_order_accrual.sql",
	}, names)
}
