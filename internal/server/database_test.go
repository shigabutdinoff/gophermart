package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const unavailableDatabaseDSN = "postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable"

type stubDatabasePinger struct {
	calls int
	err   error
}

func (p *stubDatabasePinger) PingContext(context.Context) error {
	p.calls++
	return p.err
}

func newObservedServer(t *testing.T, dsn string) (*Server, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.InfoLevel)
	cfg := config.Default()
	cfg.DatabaseURI = dsn
	return New(zap.New(core), cfg), logs
}

func newServerWithDatabase(t *testing.T) (*Server, *sql.DB) {
	t.Helper()

	db, err := database.Connection(unavailableDatabaseDSN)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	s := New(zap.NewNop(), config.Default())
	s.swapDatabase(sqlDB)
	return s, sqlDB
}

func TestInitDatabase_EmptyDSN(t *testing.T) {
	s, logs := newObservedServer(t, "")
	s.initDatabase(context.Background())
	assert.Nil(t, s.currentDatabase())
	require.Equal(t, 1, logs.FilterMessage("Не удалось открыть соединение с БД").Len())
}

func TestInitDatabase_MalformedDSNDoesNotLeakCredentials(t *testing.T) {
	s, logs := newObservedServer(t, "postgresql://user:SECRETPW@bad host:5432/praktikum")
	s.initDatabase(context.Background())
	assert.Nil(t, s.currentDatabase())
	entries := logs.FilterMessage("Не удалось открыть соединение с БД").All()
	require.Len(t, entries, 1)
	assert.Equal(t, "некорректная строка подключения к БД", entries[0].ContextMap()["error"])
}

func TestInitDatabase_UnavailableDatabaseKeepsHandleAndSkipsMigration(t *testing.T) {
	s, logs := newObservedServer(t, unavailableDatabaseDSN)
	migrationCalls := 0

	s.initDatabaseWith(context.Background(), func(string, string) (bool, error) {
		migrationCalls++
		return false, nil
	})

	require.NotNil(t, s.currentDatabase())
	t.Cleanup(s.closeDatabase)
	assert.Zero(t, migrationCalls)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "БД недоступна, миграции пропущены", entries[0].Message)
	assert.Equal(t, zap.WarnLevel, entries[0].Level)
	assert.NotEmpty(t, entries[0].ContextMap()["error"])
	assert.Zero(t, logs.FilterMessage("БД недоступна").Len())
	assert.Zero(t, logs.FilterMessage("Не удалось применить миграции").Len())
	assert.Zero(t, logs.FilterMessage("Миграции применены").Len())
	assert.Zero(t, logs.FilterMessage("Миграции: нет изменений").Len())
}

func TestCheckDatabaseAndMigrate_SuccessfulPingHandlesMigrationResult(t *testing.T) {
	migrationErr := errors.New("migration failed")
	tests := []struct {
		name            string
		applied         bool
		migrationErr    error
		expectedMessage string
		expectedLevel   zapcore.Level
	}{
		{
			name:            "migration error",
			migrationErr:    migrationErr,
			expectedMessage: "Не удалось применить миграции",
			expectedLevel:   zap.WarnLevel,
		},
		{
			name:            "migrations applied",
			applied:         true,
			expectedMessage: "Миграции применены",
			expectedLevel:   zap.InfoLevel,
		},
		{
			name:            "no migration changes",
			expectedMessage: "Миграции: нет изменений",
			expectedLevel:   zap.InfoLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const dsn = "postgresql://user:password@db:5432/gophermart"
			s, logs := newObservedServer(t, dsn)
			pinger := &stubDatabasePinger{}
			migrationCalls := 0
			var gotSourceURL string
			var gotDSN string

			s.checkDatabaseAndMigrate(
				context.Background(),
				pinger,
				func(sourceURL, migrationDSN string) (bool, error) {
					migrationCalls++
					gotSourceURL = sourceURL
					gotDSN = migrationDSN
					return tt.applied, tt.migrationErr
				},
			)

			assert.Equal(t, 1, pinger.calls)
			assert.Equal(t, 1, migrationCalls)
			assert.Equal(t, database.MigrationsURL, gotSourceURL)
			assert.Equal(t, dsn, gotDSN)

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tt.expectedMessage, entries[0].Message)
			assert.Equal(t, tt.expectedLevel, entries[0].Level)
			if tt.migrationErr != nil {
				assert.Equal(t, tt.migrationErr.Error(), entries[0].ContextMap()["error"])
			}
			assert.Zero(t, logs.FilterMessage("БД недоступна, миграции пропущены").Len())
			for _, message := range []string{
				"Не удалось применить миграции",
				"Миграции применены",
				"Миграции: нет изменений",
			} {
				if message != tt.expectedMessage {
					assert.Zero(t, logs.FilterMessage(message).Len())
				}
			}
		})
	}
}

func TestServer_Run_ClosesDatabaseOnListenError(t *testing.T) {
	s, oldDB := newServerWithDatabase(t)
	s.RunAddress = "bad::addr"

	require.Error(t, s.Run(context.Background()))
	assert.Nil(t, s.currentDatabase())
	assert.EqualError(t, oldDB.PingContext(context.Background()), "sql: database is closed")
}

func TestServer_DatabaseAccessConcurrentClose(t *testing.T) {
	s, oldDB := newServerWithDatabase(t)

	ready := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.currentDatabase()
		close(ready)
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.currentDatabase()
			}
		}
	}()

	<-ready
	s.closeDatabase()
	close(stop)
	<-done

	assert.Nil(t, s.currentDatabase())
	assert.EqualError(t, oldDB.PingContext(context.Background()), "sql: database is closed")
}
