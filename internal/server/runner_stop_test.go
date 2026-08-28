package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/jobs"
	jobsmocks "github.com/shigabutdinoff/gophermart/internal/jobs/mocks"
)

type runnerQueueController struct {
	mu      sync.Mutex
	pauses  int
	resumes int
}

func (q *runnerQueueController) pause(
	context.Context,
	string,
	*river.QueuePauseOpts,
) error {
	q.mu.Lock()
	q.pauses++
	q.mu.Unlock()

	return nil
}

func (q *runnerQueueController) resume(
	context.Context,
	string,
	*river.QueuePauseOpts,
) error {
	q.mu.Lock()
	q.resumes++
	q.mu.Unlock()

	return nil
}

func (q *runnerQueueController) counts() (int, int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.pauses, q.resumes
}

func (q *runnerQueueController) mock(t *testing.T) *jobsmocks.MockQueueController {
	t.Helper()
	controller := jobsmocks.NewMockQueueController(t)
	controller.EXPECT().QueueResume(
		mock.Anything,
		"orders",
		(*river.QueuePauseOpts)(nil),
	).RunAndReturn(q.resume).Twice()

	return controller
}
func TestRiverRunner_LateSuccessfulStartAfterStopRunsFullCleanup(t *testing.T) {
	softErr := errors.New("soft stop")
	lifecycle := newRiverLifecycleHarness()
	lifecycle.stopErr = softErr
	inStart := make(chan struct{})
	release := make(chan struct{})
	lifecycle.startHook = func() {
		close(inStart)
		<-release
	}
	throttle := &throttleLifecycleHarness{stopCalled: make(chan struct{}, 1)}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
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
		require.FailNow(t, "Stop ответил до полного late-start cleanup", "error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.ErrorIs(t, receiveWithin(t, startDone), context.Canceled)
	stopErr := receiveWithin(t, stopDone)
	require.ErrorIs(t, stopErr, context.DeadlineExceeded)
	require.ErrorIs(t, stopErr, softErr)
	receiveWithin(t, throttle.stopCalled)
	require.EqualError(t, runner.Stop(context.Background()), stopErr.Error())

	assert.Equal(t, []string{"start", "stopped", "stop", "stop_and_cancel"}, lifecycle.callLog())
	_, stopCalls := throttle.callCounts()
	assert.Equal(t, 1, stopCalls)
}

func TestRiverRunner_RepeatedStopReturnsStoredResultWithoutDuplicateCleanup(t *testing.T) {
	softErr := errors.New("soft stop")
	hardErr := errors.New("hard stop")
	throttleErr := errors.New("throttle stop")
	lifecycle := newRiverLifecycleHarness()
	lifecycle.stopErr = softErr
	lifecycle.hardStopErr = hardErr
	lifecycle.closeOnStop = true
	throttle := &throttleLifecycleHarness{stopErr: throttleErr}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))

	firstErr := runner.Stop(context.Background())
	secondErr := runner.Stop(context.Background())
	runner.CancelStart()

	require.ErrorIs(t, firstErr, softErr)
	require.ErrorIs(t, firstErr, hardErr)
	require.ErrorIs(t, firstErr, throttleErr)
	assert.Equal(t, firstErr, secondErr)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop_and_cancel"))
	_, stopCalls := throttle.callCounts()
	assert.Equal(t, 1, stopCalls)
	require.ErrorIs(t, runner.Start(context.Background()), context.Canceled)
}

func TestRiverRunner_ConcurrentStopCallersShareOnePipelineResult(t *testing.T) {
	throttleErr := errors.New("throttle stop")
	lifecycle := newRiverLifecycleHarness()
	throttle := &throttleLifecycleHarness{stopErr: throttleErr}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	firstCtx := context.WithValue(context.Background(), runnerContextKey{}, "first stop")
	secondCtx := context.WithValue(context.Background(), runnerContextKey{}, "second stop")

	firstDone := make(chan error, 1)
	go func() { firstDone <- runner.Stop(firstCtx) }()
	receiveWithin(t, lifecycle.stopCalled)
	secondDone := make(chan error, 1)
	go func() { secondDone <- runner.Stop(secondCtx) }()
	lifecycle.closeStopped()

	firstErr := receiveWithin(t, firstDone)
	secondErr := receiveWithin(t, secondDone)
	require.ErrorIs(t, firstErr, throttleErr)
	assert.Equal(t, firstErr, secondErr)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	_, stopCalls := throttle.callCounts()
	assert.Equal(t, 1, stopCalls)
	assert.Same(t, firstCtx, lifecycle.stopCtx)
	assert.Same(t, firstCtx, throttle.snapshot().stopCtx)
}

func TestRiverRunner_GracefulStopWaitsForStopped(t *testing.T) {
	events := &callRecorder{}
	lifecycle := newRiverLifecycleHarness()
	lifecycle.external = events
	throttle := &throttleLifecycleHarness{external: events}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))

	done := make(chan error, 1)
	go func() { done <- runner.Stop(context.Background()) }()
	receiveWithin(t, lifecycle.stopCalled)
	select {
	case <-done:
		require.FailNow(t, "Stop вернулся до закрытия Stopped")
	default:
	}

	lifecycle.closeStopped()
	require.NoError(t, receiveWithin(t, done))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	assert.Equal(t, []string{"resume", "start", "stopped", "stop", "throttle_stop"}, events.snapshot())
}

func TestRiverRunner_ExpiredContextStillAttemptsEveryCleanupStage(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.waitForStopContext = true
	throttle := &throttleLifecycleHarness{}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runner.Stop(stopCtx)

	require.ErrorIs(t, err, context.Canceled)
	_, throttleStops := throttle.callCounts()
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stop_and_cancel"))
	assert.Equal(t, 1, throttleStops)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	assert.Same(t, stopCtx, lifecycle.stopCtx)
	assert.Same(t, stopCtx, lifecycle.hardStopCtx)
	assert.Same(t, stopCtx, throttle.snapshot().stopCtx)
}

func TestRiverRunner_AlreadyClosedStoppedChannelDoesNotWait(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	throttle := &throttleLifecycleHarness{}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	lifecycle.closeStopped()
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, runner.Stop(stopCtx))

	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	assert.Same(t, stopCtx, throttle.snapshot().stopCtx)
}

func TestRiverRunner_StopAfterUnexpectedStopSkipsRiverCleanup(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.startContextDone = make(chan struct{})
	throttle := &throttleLifecycleHarness{}
	runner := newMockedRiverRunner(t, lifecycle, throttle)
	require.NoError(t, runner.Start(context.Background()))
	lifecycle.closeStopped()
	receiveWithin(t, lifecycle.startContextDone)

	require.NoError(t, runner.Stop(context.Background()))

	assert.Zero(t, countCalls(lifecycle.callLog(), "stop"))
	assert.Zero(t, countCalls(lifecycle.callLog(), "stop_and_cancel"))
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "stopped"))
	_, throttleStops := throttle.callCounts()
	assert.Equal(t, 1, throttleStops)
}

func TestRiverRunner_ConcurrentStopDuringLateStartSharesFinalResult(t *testing.T) {
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	lifecycle.startContextDone = make(chan struct{})
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

	firstCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { firstDone <- runner.Stop(firstCtx) }()
	receiveWithin(t, lifecycle.startContextDone)
	secondDone := make(chan error, 1)
	go func() { secondDone <- runner.Stop(context.Background()) }()
	<-firstCtx.Done()

	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			require.FailNow(t, "Stop waiter ответил до общего late-start cleanup", "error: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}

	close(release)
	require.ErrorIs(t, receiveWithin(t, startDone), context.Canceled)
	firstErr := receiveWithin(t, firstDone)
	secondErr := receiveWithin(t, secondDone)
	require.ErrorIs(t, firstErr, context.DeadlineExceeded)
	require.EqualError(t, secondErr, firstErr.Error())
	require.EqualError(t, runner.Stop(context.Background()), firstErr.Error())
}

func TestRiverRunner_StopLatchesThrottleBeforeFuturePause(t *testing.T) {
	queue := &runnerQueueController{}
	throttle := jobs.NewThrottle(zap.NewNop(), "orders")
	throttle.Attach(queue.mock(t))
	lifecycle := newRiverLifecycleHarness()
	lifecycle.closeOnStop = true
	runner := newRiverRunner(lifecycle.mock(t), throttle)
	require.NoError(t, runner.Start(context.Background()))

	require.NoError(t, runner.Stop(context.Background()))
	throttle.Pause(context.Background(), time.Minute)

	pauses, resumes := queue.counts()
	assert.Zero(t, pauses)
	assert.Equal(t, 2, resumes, "startup Resume and shutdown cleanup each clear persisted pause")
}
