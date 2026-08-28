package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRiverRunner_StopDuringBlockedResumeSkipsRiverAndTerminates(t *testing.T) {
	resumeEntered := make(chan struct{})
	releaseResume := make(chan struct{})
	throttle := &throttleLifecycleHarness{
		resumeHook: func(context.Context) {
			close(resumeEntered)
			<-releaseResume
		},
	}
	lifecycle := newRiverLifecycleHarness()
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	startDone := make(chan error, 1)
	go func() { startDone <- runner.Start(context.Background()) }()
	receiveWithin(t, resumeEntered)
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.Stop(stopCtx) }()
	<-stopCtx.Done()
	select {
	case err := <-stopDone:
		require.FailNow(t, "Stop ответил до завершения startup Resume", "error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseResume)
	require.ErrorIs(t, receiveWithin(t, startDone), context.Canceled)
	stopErr := receiveWithin(t, stopDone)
	require.ErrorIs(t, stopErr, context.DeadlineExceeded)
	require.EqualError(t, runner.Stop(context.Background()), stopErr.Error())

	assert.Zero(t, countCalls(lifecycle.callLog(), "start"))
	_, stopCalls := throttle.callCounts()
	assert.Equal(t, 1, stopCalls)
}

func TestRiverRunner_StartedStopWaitsForThrottleCleanupAfterDeadline(t *testing.T) {
	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	throttle := &throttleLifecycleHarness{
		stopHook: func(context.Context) {
			close(cleanupEntered)
			<-releaseCleanup
		},
	}
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.Stop(stopCtx) }()
	receiveWithin(t, cleanupEntered)
	<-stopCtx.Done()

	returnedEarly := false
	select {
	case <-stopDone:
		returnedEarly = true
	default:
	}
	close(releaseCleanup)

	if returnedEarly {
		assert.Fail(t, "started Stop вернулся до завершения throttle cleanup")

		return
	}
	require.NoError(t, receiveWithin(t, stopDone))
}

func TestRiverRunner_StopLosesEarlyReturnAfterStartupAnswers(t *testing.T) {
	inStart := make(chan struct{})
	releaseStart := make(chan struct{})
	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	lifecycle.startContextDone = make(chan struct{})
	lifecycle.startHook = func() {
		close(inStart)
		<-releaseStart
	}
	throttle := &throttleLifecycleHarness{
		stopHook: func(context.Context) {
			close(cleanupEntered)
			<-releaseCleanup
		},
	}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	startDone := make(chan error, 1)
	go func() { startDone <- runner.Start(context.Background()) }()
	receiveWithin(t, inStart)
	stopCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.Stop(stopCtx) }()
	receiveWithin(t, lifecycle.startContextDone)
	close(releaseStart)
	require.ErrorIs(t, receiveWithin(t, startDone), context.Canceled)
	receiveWithin(t, cleanupEntered)
	<-stopCtx.Done()

	returnedEarly := false
	select {
	case <-stopDone:
		returnedEarly = true
	default:
	}
	close(releaseCleanup)

	if returnedEarly {
		assert.Fail(t, "Stop вернулся по deadline после перехода startup → cleanup")

		return
	}
	require.NoError(t, receiveWithin(t, stopDone))
}

func TestRiverRunner_StopTimeoutEscalatesAndWaitsForStopped(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.waitForStopContext = true
	throttle := &throttleLifecycleHarness{}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := runner.Stop(stopCtx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, throttleStops := throttle.callCounts()
	assert.Equal(t, 1, throttleStops)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop_and_cancel"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	assert.Same(t, stopCtx, lifecycle.stopCtx)
	assert.Same(t, stopCtx, lifecycle.hardStopCtx)
	assert.Same(t, stopCtx, throttle.snapshot().stopCtx)
	assert.Equal(t, lifecycle.stopDeadline, lifecycle.hardStopDeadline)
	assert.Equal(t, lifecycle.stopDeadline, throttle.snapshot().stopDeadline)
}

func TestRiverRunner_WedgedQueueStopsWaitingAtBoundedDeadline(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	throttle := &throttleLifecycleHarness{}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runner.Stop(stopCtx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "wait job queue stop", "застрявшая очередь не держит процесс")
	_, throttleStops := throttle.callCounts()
	assert.Equal(t, 1, throttleStops)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	assert.Same(t, stopCtx, throttle.snapshot().stopCtx, "пауза снимается тем же истёкшим контекстом")
}

func TestRiverRunner_BlockedThrottleCleanupReturnsAtBoundedDeadline(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	throttle := &throttleLifecycleHarness{waitForStopContext: true}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	startedAt := time.Now()
	err := runner.Stop(stopCtx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(startedAt), time.Second)
	assert.Same(t, stopCtx, throttle.snapshot().stopCtx)
	assert.True(t, throttle.snapshot().stopCtxWasLive)
}

func TestRiverRunner_StopWaitsOutStartInProgress(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	throttle := &throttleLifecycleHarness{}
	inStart := make(chan struct{})
	release := make(chan struct{})
	lifecycle.startHook = func() {
		close(inStart)
		<-release
	}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	startDone := make(chan error, 1)
	go func() { startDone <- runner.Start(context.Background()) }()
	<-inStart

	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.Stop(context.Background()) }()
	select {
	case <-stopDone:
		require.FailNow(t, "остановка приняла очередь на полпути подъёма за неподнятую")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	require.ErrorIs(t, <-startDone, context.Canceled)
	require.NoError(t, <-stopDone)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
}

func TestRiverRunner_StopDeadlineWaitsForLateStartCleanup(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	inStart := make(chan struct{})
	release := make(chan struct{})
	lifecycle.startHook = func() {
		close(inStart)
		<-release
	}
	runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})
	startDone := make(chan error, 1)
	go func() { startDone <- runner.Start(context.Background()) }()
	<-inStart

	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.Stop(stopCtx) }()
	<-stopCtx.Done()
	select {
	case err := <-stopDone:
		require.FailNow(t, "Stop ответил до late-start cleanup", "error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	require.ErrorIs(t, receiveWithin(t, startDone), context.Canceled)
	require.ErrorIs(t, receiveWithin(t, stopDone), context.DeadlineExceeded)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
}

func TestRiverRunner_LateStartStoresExpiredStopResult(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	inStart := make(chan struct{})
	release := make(chan struct{})
	lifecycle.startHook = func() {
		close(inStart)
		<-release
	}
	runner := newMockedRiverRunner(t, lifecycle, &throttleLifecycleHarness{})
	startDone := make(chan error, 1)
	go func() { startDone <- runner.Start(context.Background()) }()
	receiveWithin(t, inStart)
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.Stop(stopCtx) }()
	<-stopCtx.Done()
	select {
	case err := <-stopDone:
		require.FailNow(t, "Stop ответил до сохранения late-start cleanup result", "error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.ErrorIs(t, receiveWithin(t, startDone), context.Canceled)
	stopErr := receiveWithin(t, stopDone)
	require.ErrorIs(t, stopErr, context.DeadlineExceeded)

	require.EqualError(t, runner.Stop(context.Background()), stopErr.Error())
}
