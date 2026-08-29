package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestThrottle_LazilyStartsAndTerminatesActorLoop(t *testing.T) {
	throttle, _, lifecycle := newObservedThrottleWithLifecycle(t, zap.NewNop())
	queue := newPausedQueue(t, 0, 1)
	requireNoThrottleValue(t, lifecycle)

	require.NoError(t, throttle.Attach(queue.controller))
	require.Equal(t, throttleLoopStarted, waitThrottleValue(t, lifecycle))
	require.NoError(t, throttle.Stop(context.Background()))
	waitThrottleValue(t, queue.resumeCalls)
	require.Equal(t, throttleLoopTerminated, waitThrottleValue(t, lifecycle))

	throttle.Pause(context.Background(), time.Minute)
	require.NoError(t, throttle.Resume(context.Background()))
	require.NoError(t, throttle.Stop(context.Background()))
	require.ErrorContains(t, throttle.Attach(newPausedQueue(t, 0, 0).controller), "already stopped")
	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)
	requireNoThrottleValue(t, lifecycle)
}

func TestThrottleStopCompletionBroadcastsImmutableResult(t *testing.T) {
	stopErr := errors.New("stop")
	completion := newThrottleStopCompletion()
	const waiters = 16
	results := make(chan error, waiters)
	for range waiters {
		go func() { results <- completion.wait() }()
	}
	requireNoThrottleValue(t, results)

	completion.resolve(stopErr)
	for range waiters {
		require.ErrorIs(t, waitThrottleValue(t, results), stopErr)
	}
	require.ErrorIs(t, completion.wait(), stopErr)
}

func TestThrottle_StopCancelsBlockedAutomaticResume(t *testing.T) {
	block := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(block) }) })
	queue := newPausedQueue(t, 1, 2)
	queue.setResumeBlock(block)
	core, logs := observer.New(zap.ErrorLevel)
	throttle, clock, lifecycle := newObservedThrottleWithLifecycle(t, zap.New(core))
	require.NoError(t, throttle.Attach(queue.controller))
	require.Equal(t, throttleLoopStarted, waitThrottleValue(t, lifecycle))

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)
	clock.Advance(time.Minute)
	waitThrottleValue(t, queue.resumeCalls)
	queue.setResumeBlock(nil)
	pauseDone := make(chan struct{})
	go func() {
		throttle.Pause(context.Background(), 2*time.Minute)
		close(pauseDone)
	}()
	requireNoThrottleValue(t, pauseDone)

	stopDone := make(chan error, 1)
	go func() { stopDone <- throttle.Stop(context.Background()) }()

	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(block) })
		<-stopDone
		require.FailNow(t, "Stop did not cancel a blocked automatic resume")
	}
	waitThrottleValue(t, pauseDone)
	require.Equal(t, throttleLoopTerminated, waitThrottleValue(t, lifecycle))
	assert.Zero(t, logs.FilterMessage("Не удалось автоматически вернуть опрос расчёта к работе").Len())
}

func TestThrottle_StopClearsPersistedPauseAndIgnoresFuturePause(t *testing.T) {
	queue := newPausedQueue(t, 1, 1)
	throttle, clock := newDeterministicThrottle(t, queue)
	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)

	require.NoError(t, throttle.Stop(context.Background()))
	waitThrottleValue(t, queue.resumeCalls)
	clock.Advance(time.Hour)
	throttle.Pause(context.Background(), 2*time.Minute)
	require.NoError(t, throttle.Resume(context.Background()))

	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)
	paused, resumed := queue.counts()
	assert.Equal(t, 1, paused)
	assert.Equal(t, 1, resumed)
}

func TestThrottle_StopTreatsMissingQueueAsClean(t *testing.T) {
	queue := newPausedQueue(t, 0, 1)
	queue.resumeErr = river.ErrNotFound
	core, logs := observer.New(zap.ErrorLevel)
	throttle := NewThrottle(zap.New(core), "orders", time.Now)
	require.NoError(t, throttle.Attach(queue.controller))

	require.NoError(t, throttle.Stop(context.Background()))
	assert.Zero(t, logs.Len())
}

func TestThrottle_ConcurrentStopWaitsAndReturnsStoredResult(t *testing.T) {
	resumeErr := errors.New("resume")
	release := make(chan struct{})
	queue := newPausedQueue(t, 0, 1)
	queue.resumeErr = resumeErr
	queue.resumeBlock = release
	throttle, _ := newDeterministicThrottle(t, queue)
	first := make(chan error, 1)
	second := make(chan error, 1)

	go func() { first <- throttle.Stop(context.Background()) }()
	waitThrottleValue(t, queue.resumeCalls)
	go func() { second <- throttle.Stop(context.Background()) }()
	requireNoThrottleValue(t, second)
	close(release)

	require.ErrorIs(t, waitThrottleValue(t, first), resumeErr)
	require.ErrorIs(t, waitThrottleValue(t, second), resumeErr)
	require.ErrorIs(t, throttle.Stop(context.Background()), resumeErr)
	_, resumed := queue.counts()
	assert.Equal(t, 1, resumed)
}

func TestThrottle_UnattachedStopIsHarmlessAndDoesNotCreateTimer(t *testing.T) {
	clock := clockwork.NewFakeClockAt(throttleClockStart)
	var timerFactoryCalls atomic.Int32
	throttle := newThrottle(
		zap.NewNop(),
		"orders",
		clock.Now,
		func(delay time.Duration, fire func()) timerHandle {
			timerFactoryCalls.Add(1)

			return clock.AfterFunc(delay, fire)
		},
		nil,
	)

	throttle.Pause(context.Background(), time.Minute)
	require.NoError(t, throttle.Stop(context.Background()))
	require.NoError(t, throttle.Stop(context.Background()))
	assert.Zero(t, timerFactoryCalls.Load())
}

func TestThrottle_AttachReportsUnusableWiring(t *testing.T) {
	throttle := NewThrottle(zap.NewNop(), "orders", time.Now)

	require.ErrorContains(t, throttle.Attach(nil), "nil queue controller")

	queue := newPausedQueue(t, 0, 1)
	require.NoError(t, throttle.Attach(queue.controller))
	require.ErrorContains(t, throttle.Attach(newPausedQueue(t, 0, 0).controller), "already attached")

	require.NoError(t, throttle.Stop(context.Background()))
	require.ErrorContains(t, throttle.Attach(newPausedQueue(t, 0, 0).controller), "already stopped")
	require.ErrorContains(t, throttle.Attach(nil), "nil queue controller")
}
