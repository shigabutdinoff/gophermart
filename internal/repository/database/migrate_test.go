package database

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unreachableDSN = "postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable"

type stubMigrator struct {
	calls int
	err   error
}

func (s *stubMigrator) Up() error {
	s.calls++
	return s.err
}

func TestMigrate_MissingSourceDir(t *testing.T) {
	missing := "file://" + filepath.Join(t.TempDir(), "nope")

	applied, err := Migrate(missing, unreachableDSN)

	require.Error(t, err)
	assert.False(t, applied)
}

func TestMigrate_DatabaseUnavailable(t *testing.T) {
	empty := "file://" + t.TempDir()

	applied, err := Migrate(empty, unreachableDSN)

	require.Error(t, err)
	assert.False(t, applied)
}

func TestMigrateUp_EmptySourceIsNoOp(t *testing.T) {
	m := stubMigrator{err: &os.PathError{
		Op:  "first",
		Err: os.ErrNotExist,
	}}

	applied, err := migrateUp(&m)

	require.NoError(t, err)
	assert.False(t, applied)
	assert.Equal(t, 1, m.calls)
}

func TestMigrateUp_MissingCurrentVersionIsError(t *testing.T) {
	m := stubMigrator{err: &os.PathError{
		Op:  "next",
		Err: os.ErrNotExist,
	}}

	applied, err := migrateUp(&m)

	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.False(t, applied)
	assert.Equal(t, 1, m.calls)
}

func TestMigrateUp_NoChanges(t *testing.T) {
	m := stubMigrator{err: migrate.ErrNoChange}

	applied, err := migrateUp(&m)

	require.NoError(t, err)
	assert.False(t, applied)
	assert.Equal(t, 1, m.calls)
}

func TestMigrateUp_AppliesMigrations(t *testing.T) {
	m := stubMigrator{}

	applied, err := migrateUp(&m)

	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, 1, m.calls)
}

func TestMigrateUp_ReturnsMigrationError(t *testing.T) {
	migrationErr := errors.New("migration failed")
	m := stubMigrator{err: migrationErr}

	applied, err := migrateUp(&m)

	require.Error(t, err)
	assert.Same(t, migrationErr, err)
	assert.False(t, applied)
	assert.Equal(t, 1, m.calls)
}
