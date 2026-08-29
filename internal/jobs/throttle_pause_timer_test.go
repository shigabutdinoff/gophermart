package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestThrottle_AutomaticallyResumesAtDeadline(t *testing.T) {
	queue := newPausedQueue(t, 1, 2)
	throttle, clock := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)
	clock.Advance(time.Minute - time.Nanosecond)
	requireNoThrottleValue(t, queue.resumeCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.resumeCalls)

	paused, resumed := queue.counts()
	assert.Equal(t, 1, paused)
	assert.Equal(t, 1, resumed)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_SerializesConcurrentQueueCalls(t *testing.T) {
	queue := newPausedQueue(t, 1, 2)
	pauseRelease := make(chan struct{})
	queue.setPauseBlock(pauseRelease)
	throttle, _ := newDeterministicThrottle(t, queue)
	pauseDone := make(chan struct{})
	resumeDone := make(chan error, 1)

	go func() {
		throttle.Pause(context.Background(), time.Minute)
		close(pauseDone)
	}()
	waitThrottleValue(t, queue.pauseCalls)
	go func() { resumeDone <- throttle.Resume(context.Background()) }()
	requireNoThrottleValue(t, queue.resumeCalls)
	requireNoThrottleValue(t, pauseDone)

	close(pauseRelease)
	waitThrottleValue(t, pauseDone)
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, waitThrottleValue(t, resumeDone))
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_TimerCallbackOnlyDeliversActorEvent(t *testing.T) {
	resumeRelease := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(resumeRelease) }) })
	queue := newPausedQueue(t, 1, 2)
	queue.setResumeBlock(resumeRelease)
	clock := clockwork.NewFakeClockAt(throttleClockStart)
	callbackReturned := make(chan struct{}, 1)
	throttle := newThrottle(
		zap.NewNop(),
		"orders",
		clock.Now,
		newThrottleTimerFactory(clock, callbackReturned),
		nil,
	)
	require.NoError(t, throttle.Attach(queue.controller))

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)
	clock.Advance(time.Minute)
	waitThrottleValue(t, queue.resumeCalls)
	waitThrottleValue(t, callbackReturned)

	releaseOnce.Do(func() { close(resumeRelease) })
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_ExtendedDeadlineIsNotResumedByStaleTick(t *testing.T) {
	queue := newPausedQueue(t, 2, 2)
	throttle, clock := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)
	clock.Advance(10 * time.Second)
	throttle.Pause(context.Background(), 2*time.Minute)
	waitThrottleValue(t, queue.pauseCalls)

	throttle.emit(throttleCommand{kind: throttleTimerFired})
	requireNoThrottleValue(t, queue.resumeCalls)

	clock.Advance(2*time.Minute - time.Nanosecond)
	requireNoThrottleValue(t, queue.resumeCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_EarlierDeadlineDoesNotShortenPause(t *testing.T) {
	queue := newPausedQueue(t, 1, 1)
	throttle, clock := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), 2*time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)
	throttle.Pause(context.Background(), time.Minute)

	requireNoThrottleValue(t, queue.pauseCalls)
	clock.Advance(time.Minute)
	requireNoThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}
