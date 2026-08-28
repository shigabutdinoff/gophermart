package server

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestServer_StopQueueForwardsSharedContext(t *testing.T) {
	lifecycle := newShutdownRiverLifecycle()
	lifecycle.closeOnStop = true
	throttle := &shutdownThrottleLifecycle{}
	runner := newMockedShutdownRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	s := &Server{
		logger: zap.NewNop(),
		deps: deps{
			runner: runner,
		},
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	s.stopQueue(stopCtx)

	assert.Same(t, stopCtx, lifecycle.stopCtx)
	assert.Same(t, stopCtx, throttle.stopCtx)
}

func TestServer_ShutdownUsesOneAbsoluteDeadlineAndClosesDatabaseLast(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()
	mock.ExpectPing()
	mock.ExpectClose()

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	router := chi.NewRouter()
	router.Get("/hold", func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})
	events := &shutdownCallRecorder{}
	lifecycle := newShutdownRiverLifecycle()
	lifecycle.external = events
	lifecycle.waitForStopContext = true
	databaseAtThrottleStop := make(chan error, 1)
	throttleReachedDeadline := make(chan struct{})
	releaseThrottleCleanup := make(chan struct{})
	throttle := &shutdownThrottleLifecycle{
		external:           events,
		waitForStopContext: true,
		stopAfterContext: func(context.Context) {
			close(throttleReachedDeadline)
			<-releaseThrottleCleanup
			databaseAtThrottleStop <- sqlDB.PingContext(context.Background())
			events.record("cleanup_db_open")
		},
	}
	shutdownTimeout := 160 * time.Millisecond
	s := &Server{
		router:          router,
		logger:          zap.NewNop(),
		shutdownTimeout: shutdownTimeout,
		sqlDB:           sqlDB,
		deps: deps{
			runner: newMockedShutdownRunner(t, lifecycle, throttle),
		},
	}
	s.runAddress = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	done := startServer(t, ctx, s)
	receiveWithin(t, lifecycle.startCalled)
	go func() {
		resp, err := http.Get("http://" + s.Addr() + "/hold")
		if err == nil {
			resp.Body.Close()
		}
	}()

	<-started
	shutdownStartedAt := time.Now()
	cancel()
	receiveWithin(t, throttleReachedDeadline)
	var earlyRunErr error
	serverReturnedBeforeCleanup := false
	select {
	case earlyRunErr = <-done:
		serverReturnedBeforeCleanup = true
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseThrottleCleanup)
	var runErr error
	if serverReturnedBeforeCleanup {
		runErr = earlyRunErr
	} else {
		runErr = waitDone(t, done)
	}
	elapsed := time.Since(shutdownStartedAt)

	lifecycle.mu.Lock()
	stopCtx := lifecycle.stopCtx
	hardStopCtx := lifecycle.hardStopCtx
	stopDeadline := lifecycle.stopDeadline
	stopCalledAt := lifecycle.stopCalledAt
	stopCtxWasLive := lifecycle.stopCtxWasLive
	lifecycle.mu.Unlock()

	require.NoError(t, runErr, "ошибки graceful shutdown не меняют код выхода по сигналу")
	assert.False(t, serverReturnedBeforeCleanup, "сервер закрыл DB до завершения throttle cleanup")
	require.NoError(t, receiveWithin(t, databaseAtThrottleStop))
	require.Eventually(t, func() bool {
		return mock.ExpectationsWereMet() == nil
	}, time.Second, time.Millisecond)
	assert.GreaterOrEqual(t, stopCalledAt.Sub(shutdownStartedAt), shutdownTimeout/2-25*time.Millisecond)
	assert.LessOrEqual(t, elapsed, shutdownTimeout+50*time.Millisecond)
	assert.True(t, stopCtxWasLive, "HTTP тратит лишь первую половину общего срока")
	assert.WithinDuration(t, shutdownStartedAt.Add(shutdownTimeout), stopDeadline, 25*time.Millisecond)
	assert.Same(t, stopCtx, hardStopCtx)
	assert.Same(t, stopCtx, throttle.stopCtx)
	orderedLifecycle := slices.DeleteFunc(events.snapshot(), func(call string) bool {
		return call == "stopped"
	})
	assert.Equal(
		t,
		[]string{"resume", "start", "stop", "stop_and_cancel", "throttle_stop", "cleanup_db_open"},
		orderedLifecycle,
	)
}
