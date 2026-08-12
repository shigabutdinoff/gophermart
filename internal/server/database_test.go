package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const unavailableDatabaseDSN = "postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable"

func newPingMock(t *testing.T, pingErr error) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing().WillReturnError(pingErr)
	return db, mock
}

func newObservedServer(t *testing.T, dsn string) (*Server, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.InfoLevel)
	cfg := config.Default()
	cfg.DatabaseURI = dsn
	return mustNew(t, zap.New(core), cfg), logs
}

func newServerWithDatabase(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	gormDB, err := database.Connection(unavailableDatabaseDSN)
	require.NoError(t, err)
	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	server, err := newServer(zap.NewNop(), config.Default(), time.Now, gormDB, sqlDB)
	require.NoError(t, err)
	return server, sqlDB
}

func TestOpenDatabase_EmptyDSN(t *testing.T) {
	s, logs := newObservedServer(t, "")
	assert.Nil(t, s.sqlDB)
	require.Equal(t, 1, logs.FilterMessage("Не удалось открыть соединение с БД").Len())
}

func TestOpenDatabase_MalformedDSNDoesNotLeakCredentials(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:SECRETPW@bad host:5432/praktikum")
	assert.Nil(t, s.sqlDB)
	entries := logs.FilterMessage("Не удалось открыть соединение с БД").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "некорректная строка подключения к БД", entries[0].ContextMap()["error"])
}

func TestInitDatabase_UnavailableDatabaseKeepsHandleAndSkipsMigration(t *testing.T) {
	s, logs := newObservedServer(t, unavailableDatabaseDSN)
	migrationCalls := 0
	s.initDatabase(context.Background(), func(context.Context, *sql.DB) error {
		migrationCalls++
		return nil
	})
	require.NotNil(t, s.sqlDB)
	t.Cleanup(s.closeDatabase)
	assert.Zero(t, migrationCalls)
	skipped := logs.FilterMessage("БД недоступна, миграции пропущены").All()
	require.Len(t, skipped, 1)
	assert.Equal(t, zap.ErrorLevel, skipped[0].Level)
}

func TestInitDatabase_SuccessfulPingHandlesMigrationResult(t *testing.T) {
	migrationErr := errors.New("migration failed")
	tests := []struct {
		name            string
		migrationErr    error
		expectedMessage string
		expectedLevel   zapcore.Level
	}{
		{"migration error", migrationErr, "Не удалось применить миграции", zap.ErrorLevel},
		{"migrations applied", nil, "Миграции выполнены", zap.InfoLevel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, logs := newObservedServer(t, "postgresql://user:password@db:5432/gophermart")
			handle, mock := newPingMock(t, nil)
			s.sqlDB = handle
			migrationCalls := 0
			s.initDatabase(context.Background(), func(_ context.Context, db *sql.DB) error {
				migrationCalls++
				assert.Same(t, handle, db)
				return tt.migrationErr
			})
			require.NoError(t, mock.ExpectationsWereMet())
			assert.Equal(t, 1, migrationCalls)
			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tt.expectedMessage, entries[0].Message)
			assert.Equal(t, tt.expectedLevel, entries[0].Level)
		})
	}
}

func TestOpenDatabase_AppliesPoolLimits(t *testing.T) {
	s, _ := newObservedServer(t, unavailableDatabaseDSN)
	require.NotNil(t, s.sqlDB)
	t.Cleanup(s.closeDatabase)
	assert.Equal(t, databaseMaxConns, s.sqlDB.Stats().MaxOpenConnections)
}

func TestServer_Run_ClosesDatabaseOnListenError(t *testing.T) {
	s, oldDB := newServerWithDatabase(t)
	s.RunAddress = "bad::addr"
	require.Error(t, s.Run(context.Background()))
	assert.EqualError(t, oldDB.PingContext(context.Background()), "sql: database is closed")
}
