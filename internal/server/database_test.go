package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const unavailableDatabaseDSN = "postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable"

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

func TestInitDatabase_UnavailableDatabaseKeepsHandle(t *testing.T) {
	s, logs := newObservedServer(t, unavailableDatabaseDSN)

	s.initDatabase(context.Background())

	require.NotNil(t, s.currentDatabase())
	t.Cleanup(s.closeDatabase)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "БД недоступна", entries[0].Message)
	assert.Equal(t, zap.WarnLevel, entries[0].Level)
	assert.NotEmpty(t, entries[0].ContextMap()["error"])
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
