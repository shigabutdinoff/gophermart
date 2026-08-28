package server

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestServer_InterruptCancelsBlockedRunnerStartAndClosesDatabase(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()
	mock.ExpectClose()

	lifecycle := newShutdownRiverLifecycle()
	lifecycle.waitForStartContext = true
	t.Cleanup(lifecycle.releaseStart)
	s := &Server{
		router:          chi.NewRouter(),
		logger:          zap.NewNop(),
		shutdownTimeout: time.Second,
		sqlDB:           sqlDB,
		deps: deps{
			runner: newMockedShutdownRunner(t, lifecycle, &shutdownThrottleLifecycle{}),
		},
	}
	s.runAddress = "127.0.0.1:0"
	signalCtx, cancel := context.WithCancel(context.Background())
	done := startServer(t, signalCtx, s)
	receiveWithin(t, lifecycle.startCalled)

	cancel()
	require.NoError(t, receiveWithin(t, done))
	lifecycle.mu.Lock()
	require.ErrorIs(t, lifecycle.startCtx.Err(), context.Canceled)
	lifecycle.mu.Unlock()
	assert.Equal(
		t,
		[]string{"start"},
		lifecycle.callLog(),
		"не запущенный River повторяет подъём и не ждёт Stopped",
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestServer_StartupResumeFailureCleansThrottleBeforeDatabaseClose(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()
	mock.ExpectPing()
	mock.ExpectClose()

	events := &shutdownCallRecorder{}
	databaseAtThrottleStop := make(chan error, 1)
	lifecycle := newShutdownRiverLifecycle()
	lifecycle.external = events
	shutdownTimeout := 20 * time.Millisecond
	throttle := &shutdownThrottleLifecycle{
		external:           events,
		resumeErr:          errors.New("resume queue"),
		waitForStopContext: true,
		stopHook: func(context.Context) {
			databaseAtThrottleStop <- sqlDB.PingContext(context.Background())
			events.record("cleanup_db_open")
		},
	}
	s := &Server{
		router:          chi.NewRouter(),
		logger:          zap.NewNop(),
		shutdownTimeout: shutdownTimeout,
		sqlDB:           sqlDB,
		deps: deps{
			runner: newMockedShutdownRunner(t, lifecycle, throttle),
		},
	}
	s.runAddress = "127.0.0.1:0"
	signalCtx, cancel := context.WithCancel(context.Background())
	done := startServer(t, signalCtx, s)
	require.Eventually(t, func() bool {
		return len(events.snapshot()) >= 1
	}, time.Second, time.Millisecond)
	assert.Equal(t, []string{"resume"}, slices.Compact(events.snapshot()))
	assert.Empty(t, lifecycle.callLog(), "resume failure skips River Start and Stopped")

	cancel()
	require.NoError(t, receiveWithin(t, databaseAtThrottleStop))
	runErr := waitDone(t, done)
	require.NoError(t, runErr, "неудача остановки остаётся в логе")
	assert.True(t, throttle.stopCtxWasLive)
	require.True(t, throttle.stopHasDeadline)
	assert.True(t, throttle.stopDeadline.After(throttle.stopCalledAt))
	assert.LessOrEqual(
		t,
		throttle.stopDeadline.Sub(throttle.stopCalledAt),
		shutdownTimeout,
	)
	require.Eventually(t, func() bool {
		return mock.ExpectationsWereMet() == nil
	}, time.Second, time.Millisecond)
	assert.Equal(
		t,
		[]string{"resume", "throttle_stop", "cleanup_db_open"},
		slices.Compact(events.snapshot()),
	)
	assert.EqualError(t, sqlDB.PingContext(context.Background()), "sql: database is closed")
}
