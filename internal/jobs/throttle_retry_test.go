package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestThrottle_RepeatsFailedPause(t *testing.T) {
	queueErr := errors.New("pause")
	queue := newPausedQueue(t, 2, 2)
	queue.setPauseError(queueErr)
	core, logs := observer.New(zap.ErrorLevel)
	throttle, clock := newObservedThrottle(t, zap.New(core))
	require.NoError(t, throttle.Attach(queue.controller))

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	// без повтора воркеры продолжали бы опрашивать расчёт весь срок отказа
	waitForThrottleTimers(t, clock, 1)
	assert.Equal(t, 1, logs.FilterMessage("Не удалось придержать опрос расчёта").Len())

	queue.setPauseError(nil)
	clock.Advance(retryDelay - time.Nanosecond)
	requireNoThrottleValue(t, queue.pauseCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)

	clock.Advance(time.Minute - retryDelay - time.Nanosecond)
	requireNoThrottleValue(t, queue.resumeCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_IgnoresStaleTimerEventBeforeRearmedPauseRetry(t *testing.T) {
	queue := newPausedQueue(t, 3, 1)
	queue.setPauseError(errors.New("pause"))
	throttle, clock := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)

	throttle.Pause(context.Background(), 2*time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	throttle.emit(throttleCommand{kind: throttleTimerFired})
	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)

	queue.setPauseError(nil)
	clock.Advance(retryDelay - time.Nanosecond)
	requireNoThrottleValue(t, queue.pauseCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.pauseCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_IgnoresDuplicateTimerEventDuringAutomaticResumeRetry(t *testing.T) {
	resumeRelease := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(resumeRelease) }) })
	queue := newPausedQueue(t, 1, 3)
	queue.setResumeError(errors.New("resume"))
	queue.setResumeBlock(resumeRelease)
	throttle, clock := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)
	clock.Advance(time.Minute)
	waitThrottleValue(t, queue.resumeCalls)
	throttle.emit(throttleCommand{kind: throttleTimerFired})

	releaseOnce.Do(func() { close(resumeRelease) })
	waitForThrottleTimers(t, clock, 1)
	requireNoThrottleValue(t, queue.resumeCalls)

	queue.setResumeError(nil)
	clock.Advance(retryDelay - time.Nanosecond)
	requireNoThrottleValue(t, queue.resumeCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}

// Несостоявшуюся паузу снимать нечем: срок вышел, а очередь её не принимала.
func TestThrottle_ForgetsDeadlineOfPauseQueueNeverAccepted(t *testing.T) {
	queue := newPausedQueue(t, 1, 1)
	queue.setPauseError(errors.New("pause"))
	throttle, clock := newDeterministicThrottle(t, queue)

	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)

	clock.Advance(time.Minute)

	requireNoThrottleValue(t, queue.pauseCalls)
	requireNoThrottleValue(t, queue.resumeCalls)
	queue.setPauseError(nil)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_RepeatsAutomaticResumeAfterFailure(t *testing.T) {
	queue := newPausedQueue(t, 1, 3)
	queue.setResumeError(errors.New("resume"))
	core, logs := observer.New(zap.ErrorLevel)
	throttle, clock := newObservedThrottle(t, zap.New(core))
	require.NoError(t, throttle.Attach(queue.controller))
	throttle.Pause(context.Background(), time.Minute)
	waitThrottleValue(t, queue.pauseCalls)
	waitForThrottleTimers(t, clock, 1)

	clock.Advance(time.Minute)
	waitThrottleValue(t, queue.resumeCalls)

	// приостановленная очередь не запустит воркер, и позвать Pause
	// со свежим сроком станет некому
	waitForThrottleTimers(t, clock, 1)
	assert.Equal(t, 1, logs.Len())

	queue.setResumeError(nil)
	clock.Advance(retryDelay - time.Nanosecond)
	requireNoThrottleValue(t, queue.resumeCalls)
	clock.Advance(time.Nanosecond)
	waitThrottleValue(t, queue.resumeCalls)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_ResumeReturnsFailureToCaller(t *testing.T) {
	resumeErr := errors.New("resume")
	queue := newPausedQueue(t, 0, 2)
	queue.setResumeError(resumeErr)
	throttle, _ := newDeterministicThrottle(t, queue)

	err := throttle.Resume(context.Background())

	require.ErrorIs(t, err, resumeErr)
	waitThrottleValue(t, queue.resumeCalls)
	queue.setResumeError(nil)
	require.NoError(t, throttle.Stop(context.Background()))
}

func TestThrottle_ResumeTreatsMissingNewQueueAsAlreadyResumed(t *testing.T) {
	queue := newPausedQueue(t, 0, 2)
	queue.resumeErr = river.ErrNotFound
	core, logs := observer.New(zap.ErrorLevel)
	throttle := NewThrottle(zap.New(core), "orders", time.Now)
	require.NoError(t, throttle.Attach(queue.controller))

	require.NoError(t, throttle.Resume(context.Background()))
	assert.Zero(t, logs.Len())

	queue.resumeErr = nil
	require.NoError(t, throttle.Stop(context.Background()))
}
