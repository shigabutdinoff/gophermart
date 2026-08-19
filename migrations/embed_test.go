package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFSContainsUsersMigration(t *testing.T) {
	sql := readMigrationSQL(t, "00001_create_users.sql")
	assert.Contains(t, sql, "-- +goose Up")
	assert.Contains(t, sql, "-- +goose Down")
	assert.Contains(t, sql, "CREATE TABLE users")
	assert.Contains(t, sql, "CONSTRAINT users_login_key UNIQUE (login)")
	assert.Contains(t, sql, "password_hash TEXT NOT NULL")
	assert.Contains(t, sql, "created_at TIMESTAMPTZ NOT NULL")
	assert.Contains(t, sql, "DROP TABLE IF EXISTS users;")
}

func TestFSContainsOrdersMigration(t *testing.T) {
	sql := readMigrationSQL(t, "00002_create_orders.sql")
	assert.Contains(t, sql, "-- +goose Up")
	assert.Contains(t, sql, "-- +goose Down")
	assert.Contains(t, sql, "CREATE TABLE orders")
	assert.Contains(t, sql, "number TEXT NOT NULL")
	assert.Contains(t, sql, "number_hash BYTEA NOT NULL")
	assert.Contains(t, sql, "CONSTRAINT orders_number_hash_key UNIQUE (number_hash)")
	assert.NotContains(t, sql, "UNIQUE (number)")
	assert.Contains(t, sql, "user_id BIGINT NOT NULL REFERENCES users (id)")
	assert.Contains(t, sql, "CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED'))")
	assert.Contains(t, sql, "uploaded_at TIMESTAMPTZ NOT NULL")
	assert.Contains(t, sql, "CREATE INDEX orders_user_uploaded_idx ON orders (user_id, uploaded_at DESC, id DESC);")
	assert.Contains(t, sql, "DROP TABLE IF EXISTS orders;")
}

func TestFSContainsOrderAccrualMigration(t *testing.T) {
	sql := readMigrationSQL(t, "00003_add_order_accrual.sql")
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

func TestFSContainsWithdrawalsMigration(t *testing.T) {
	sql := readMigrationSQL(t, "00004_create_withdrawals.sql")
	assert.Contains(t, sql, "-- +goose Up")
	assert.Contains(t, sql, "-- +goose Down")
	assert.Contains(t, sql, "CREATE TABLE withdrawals")
	assert.Contains(t, sql, "user_id BIGINT NOT NULL REFERENCES users (id)")
	assert.Contains(t, sql, "CONSTRAINT withdrawals_sum_positive CHECK (sum > 0)")
	assert.Contains(t, sql, "CONSTRAINT withdrawals_order_number_key UNIQUE (order_number)")
	assert.NotContains(t, sql, "CREATE INDEX")
	assert.Contains(t, sql, "COMMENT ON COLUMN withdrawals.sum IS 'Списанные баллы в копейках';")

	up, down, found := strings.Cut(sql, "-- +goose Down")
	require.True(t, found)
	assert.NotContains(t, up, "DROP ")
	assert.NotContains(t, up, "orders_user_processed_idx")
	assert.Contains(t, down, "DROP TABLE IF EXISTS withdrawals;")
	assert.NotContains(t, down, "orders_user_processed_idx")
	assert.NotContains(t, down, "withdrawals_user_processed_idx")
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
		"00004_create_withdrawals.sql",
	}, names)
}

func readMigrationSQL(t *testing.T, name string) string {
	t.Helper()

	migration, err := FS.ReadFile(name)
	require.NoError(t, err)

	return string(migration)
}
