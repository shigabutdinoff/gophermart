package server

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

func TestServerRun_RecoversDatabasePreparationInDispatchOnlyMode(t *testing.T) {
	db, connector := newScriptedPingDatabase(t, func(int32) error {
		return database.ErrUnavailable
	})
	core, logs := observer.New(zap.WarnLevel)
	s := &Server{
		router:          chi.NewRouter(),
		logger:          zap.New(core),
		shutdownTimeout: time.Second,
		retryDelay:      time.Millisecond,
		migrateDatabase: successfulTestMigration,
		ln:              newBlockingTestListener(),
		sqlDB:           db,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	require.Eventually(t, func() bool {
		return connector.calls.Load() > databaseConnectAttempts
	}, time.Second, time.Millisecond)
	assert.Nil(t, s.deps.runner)
	require.Eventually(t, func() bool {
		return logs.FilterMessage("Повторная попытка подготовить БД").Len() > 0
	}, time.Second, time.Millisecond)

	cancel()
	require.NoError(t, waitDone(t, done))
}

func TestServerRun_StartsQueueOnlyAfterRecoveredMigrationsFinish(t *testing.T) {
	events := &databaseEventLog{}
	db, _ := newScriptedPingDatabase(t, func(call int32) error {
		events.add("ping")
		if call <= databaseConnectAttempts {
			return database.ErrUnavailable
		}

		return nil
	})
	migrationStarted := make(chan struct{})
	releaseMigration := make(chan struct{})
	lifecycle := newDatabaseRecoveryRiverLifecycle(events)
	s := &Server{
		router:          chi.NewRouter(),
		logger:          zap.NewNop(),
		shutdownTimeout: time.Second,
		retryDelay:      time.Nanosecond,
		migrateDatabase: func(ctx context.Context, _ *sql.DB) error {
			events.add("migrations_started")
			close(migrationStarted)
			select {
			case <-releaseMigration:
				events.add("migrations_finished")
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		ln:    newBlockingTestListener(),
		sqlDB: db,
		deps: deps{
			runner: newRiverRunner(
				lifecycle.mock(t),
				newDatabaseRecoveryThrottle(t, events),
			),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	receiveWithin(t, migrationStarted)
	select {
	case <-lifecycle.startCalled:
		require.FailNow(t, "River.Start вызван до завершения миграций")
	default:
	}
	close(releaseMigration)
	receiveWithin(t, lifecycle.startCalled)
	receiveWithin(t, lifecycle.stoppedCalled)
	wantPrefix := []string{
		"ping", "ping", "ping", "ping",
		"migrations_started", "migrations_finished", "resume", "start",
	}
	got := events.snapshot()
	require.GreaterOrEqual(t, len(got), len(wantPrefix))
	assert.Equal(t, wantPrefix, got[:len(wantPrefix)])

	cancel()
	require.NoError(t, waitDone(t, done))
}
