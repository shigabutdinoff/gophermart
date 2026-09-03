package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestThrottle_RepeatsFailedPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queueErr := errors.New("pause")
		queue := newPausedQueue(t, 2, 2)
		queue.setPauseError(queueErr)
		core, logs := observer.New(zap.ErrorLevel)
		throttle := newDeterministicThrottle(t, zap.New(core), queue)

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		// без повтора воркеры продолжали бы опрашивать расчёт весь срок отказа
		synctest.Wait()
		assert.Equal(t, 1, logs.FilterMessage("Не удалось придержать опрос расчёта").Len())

		queue.setPauseError(nil)
		waitThrottleValueAt(t, queue.pauseCalls, retryDelay)
		synctest.Wait()

		waitThrottleValueAt(t, queue.resumeCalls, time.Minute-retryDelay)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_IgnoresStaleTimerEventBeforeRearmedPauseRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 3, 1)
		queue.setPauseError(errors.New("pause"))
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()

		throttle.Pause(context.Background(), 2*time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		throttle.emit(throttleCommand{kind: throttleTimerFired})
		requireNoThrottleValue(t, queue.pauseCalls)
		requireNoThrottleValue(t, queue.resumeCalls)

		queue.setPauseError(nil)
		waitThrottleValueAt(t, queue.pauseCalls, retryDelay)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_IgnoresDuplicateTimerEventDuringAutomaticResumeRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resumeRelease := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(resumeRelease) })
		queue := newPausedQueue(t, 1, 3)
		queue.setResumeError(errors.New("resume"))
		queue.setResumeBlock(resumeRelease)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()
		time.Sleep(time.Minute)
		waitThrottleValue(t, queue.resumeCalls)
		throttle.emit(throttleCommand{kind: throttleTimerFired})

		releaseOnce.Do(func() { close(resumeRelease) })
		synctest.Wait()
		requireNoThrottleValue(t, queue.resumeCalls)

		queue.setResumeError(nil)
		waitThrottleValueAt(t, queue.resumeCalls, retryDelay)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

// Несостоявшуюся паузу снимать нечем: срок вышел, а очередь её не принимала.
func TestThrottle_ForgetsDeadlineOfPauseQueueNeverAccepted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 2, 1)
		queue.setPauseError(errors.New("pause"))
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		// срок переживает один повтор и обрывается на середине второго
		throttle.Pause(context.Background(), retryDelay+retryDelay/2)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()

		time.Sleep(retryDelay)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()

		time.Sleep(retryDelay)

		requireNoThrottleValue(t, queue.pauseCalls)
		requireNoThrottleValue(t, queue.resumeCalls)
		queue.setPauseError(nil)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_RepeatsAutomaticResumeAfterFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 1, 3)
		queue.setResumeError(errors.New("resume"))
		core, logs := observer.New(zap.ErrorLevel)
		throttle := newDeterministicThrottle(t, zap.New(core), queue)
		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()

		time.Sleep(time.Minute)
		waitThrottleValue(t, queue.resumeCalls)

		// приостановленная очередь не запустит воркер, и позвать Pause
		// со свежим сроком станет некому
		synctest.Wait()
		assert.Equal(t, 1, logs.Len())

		queue.setResumeError(nil)
		waitThrottleValueAt(t, queue.resumeCalls, retryDelay)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_ResumeReturnsFailureToCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resumeErr := errors.New("resume")
		queue := newPausedQueue(t, 0, 2)
		queue.setResumeError(resumeErr)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		err := throttle.Resume(context.Background())

		require.ErrorIs(t, err, resumeErr)
		waitThrottleValue(t, queue.resumeCalls)
		queue.setResumeError(nil)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_ResumeTreatsMissingNewQueueAsAlreadyResumed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 0, 2)
		queue.resumeErr = river.ErrNotFound
		core, logs := observer.New(zap.ErrorLevel)
		throttle := newDeterministicThrottle(t, zap.New(core), queue)

		require.NoError(t, throttle.Resume(context.Background()))
		assert.Zero(t, logs.Len())

		queue.resumeErr = nil
		require.NoError(t, throttle.Stop(context.Background()))
	})
}
