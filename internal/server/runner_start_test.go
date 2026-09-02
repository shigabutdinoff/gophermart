package server

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/shigabutdinoff/gophermart/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRiverRunner_FailedStartStopsPromptlyWithoutWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		startErr := errors.New("start")
		lifecycle := newRiverLifecycleHarness()
		lifecycle.startErr = startErr
		throttle := &throttleLifecycleHarness{}
		runner := newMockedRiverRunner(t, lifecycle, throttle)

		require.ErrorIs(t, runner.Start(context.Background()), startErr)
		stopCtx := context.WithValue(context.Background(), runnerContextKey{}, "shutdown")
		done := make(chan error, 1)
		go func() { done <- runner.Stop(stopCtx) }()

		require.NoError(t, testsupport.WaitValue(t, done, "Stop ждёт Stopped после неудачного Start"))
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
		assert.Same(t, stopCtx, throttle.stopContext(), "незапускавшаяся очередь снимает throttle тем же context")
	})
}

func TestRiverRunner_SuccessfulStartReturnsNilWhenContextIsCanceledAtReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newRiverLifecycleHarness()
		lifecycle.closeOnStop = true
		ctx, cancel := context.WithCancel(context.Background())
		lifecycle.startHook = cancel
		runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})

		require.NoError(t, runner.Start(ctx))
		require.NoError(t, runner.Stop(context.Background()))
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	})
}

// Свёрнутой очереди контекст держать некому: остановка его освобождает.
func TestRiverRunner_StopReleasesStartContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newRiverLifecycleHarness()
		lifecycle.closeOnStop = true
		runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})

		require.NoError(t, runner.Start(context.Background()))
		require.NoError(t, runner.Stop(context.Background()))

		lifecycle.mu.Lock()
		defer lifecycle.mu.Unlock()
		assert.True(t, lifecycle.startCtxLiveAtStop, "остановка застаёт контекст живым")
		assert.ErrorIs(t, lifecycle.startCtx.Err(), context.Canceled)
	})
}

func TestRiverRunner_ConcurrentStartWaitsForSameAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newRiverLifecycleHarness()
		lifecycle.closeOnStop = true
		inStart := make(chan struct{})
		release := make(chan struct{})
		lifecycle.startHook = func() {
			close(inStart)
			<-release
		}
		runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})
		firstDone := make(chan error, 1)
		go func() { firstDone <- runner.Start(context.Background()) }()
		<-inStart

		secondDone := make(chan error, 1)
		go func() { secondDone <- runner.Start(context.Background()) }()
		// второй Start ждёт ту же попытку, пока первая держит хук
		testsupport.RequireNoValue(t, secondDone, "второй Start ответил, не дождавшись первой попытки")
		close(release)

		require.NoError(t, <-firstDone)
		require.NoError(t, <-secondDone)
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
		require.NoError(t, runner.Stop(context.Background()))
	})
}

func TestRiverRunner_StartWhileStartedDoesNotStartRiverAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newRiverLifecycleHarness()
		lifecycle.closeOnStop = true
		runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})

		require.NoError(t, runner.Start(context.Background()))
		require.NoError(t, runner.Start(context.Background()))

		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
		require.NoError(t, runner.Stop(context.Background()))
	})
}

func TestRiverRunner_CapturesStoppedBeforePublishingStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stoppedEntered := make(chan struct{})
		releaseStopped := make(chan struct{})
		lifecycle := newRiverLifecycleHarness()
		lifecycle.closeOnStop = true
		lifecycle.stoppedHook = func() {
			close(stoppedEntered)
			<-releaseStopped
		}
		runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})
		startDone := make(chan error, 1)
		go func() { startDone <- runner.Start(context.Background()) }()
		<-stoppedEntered

		testsupport.RequireNoValue(t, startDone, "Start опубликован до получения текущего Stopped channel")
		close(releaseStopped)

		require.NoError(t, <-startDone)
		require.NoError(t, runner.Stop(context.Background()))
	})
}

func TestRiverRunner_StartResumesThrottleBeforeRiver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := &callRecorder{}
		lifecycle := newRiverLifecycleHarness()
		lifecycle.external = events
		throttle := &throttleLifecycleHarness{external: events}
		runner := newMockedRiverRunner(t, lifecycle, throttle)

		require.NoError(t, runner.Start(context.Background()))

		require.GreaterOrEqual(t, len(events.snapshot()), 2)
		assert.Equal(t, []string{"resume", "start"}, events.snapshot()[:2])
		lifecycle.closeStopped()
		require.NoError(t, runner.Stop(context.Background()))
	})
}

func TestRiverRunner_ResumeFailureSkipsRiverAndStopStillCleansThrottle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resumeErr := errors.New("resume")
		events := &callRecorder{}
		lifecycle := newRiverLifecycleHarness()
		lifecycle.external = events
		throttle := &throttleLifecycleHarness{external: events, resumeErr: resumeErr}
		runner := newMockedRiverRunner(t, lifecycle, throttle)

		require.ErrorIs(t, runner.Start(context.Background()), resumeErr)
		require.NoError(t, runner.Stop(context.Background()))

		assert.Equal(t, []string{"resume", "throttle_stop"}, events.snapshot())
		assert.NotContains(t, lifecycle.callLog(), "stopped")
	})
}
