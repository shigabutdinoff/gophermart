package jobs

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestThrottle_AutomaticallyResumesAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 1, 2)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()
		waitThrottleValueAt(t, queue.resumeCalls, time.Minute)

		paused, resumed := queue.counts()
		assert.Equal(t, 1, paused)
		assert.Equal(t, 1, resumed)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_SerializesConcurrentQueueCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 1, 2)
		pauseRelease := make(chan struct{})
		queue.setPauseBlock(pauseRelease)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)
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
	})
}

// Callback таймера ничего не решает сам: он лишь доносит событие до актора.
// Если бы он снимал срок на месте, актор не знал бы про начатый resume
// и обслужил бы следующий Pause немедленно, а не отложил.
func TestThrottle_TimerFiringDoesNotRunQueueCallInline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resumeRelease := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(resumeRelease) })
		queue := newPausedQueue(t, 2, 2)
		queue.setResumeBlock(resumeRelease)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		time.Sleep(time.Minute)
		waitThrottleValue(t, queue.resumeCalls)

		pauseDone := make(chan struct{})
		go func() {
			throttle.Pause(context.Background(), 2*time.Minute)
			close(pauseDone)
		}()
		requireNoThrottleValue(t, pauseDone)
		requireNoThrottleValue(t, queue.pauseCalls)

		releaseOnce.Do(func() { close(resumeRelease) })
		waitThrottleValue(t, pauseDone)
		waitThrottleValue(t, queue.pauseCalls)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_ExtendedDeadlineIsNotResumedByStaleTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 2, 2)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()
		time.Sleep(10 * time.Second)
		throttle.Pause(context.Background(), 2*time.Minute)
		waitThrottleValue(t, queue.pauseCalls)

		throttle.emit(throttleCommand{kind: throttleTimerFired})
		requireNoThrottleValue(t, queue.resumeCalls)

		waitThrottleValueAt(t, queue.resumeCalls, 2*time.Minute)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}

func TestThrottle_EarlierDeadlineDoesNotShortenPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 1, 1)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)

		throttle.Pause(context.Background(), 2*time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()
		throttle.Pause(context.Background(), time.Minute)

		requireNoThrottleValue(t, queue.pauseCalls)
		time.Sleep(time.Minute)
		requireNoThrottleValue(t, queue.resumeCalls)
		require.NoError(t, throttle.Stop(context.Background()))
	})
}
