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

func TestThrottle_LazilyStartsAndTerminatesActorLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		throttle := NewThrottle(zap.NewNop(), "orders")
		queue := newPausedQueue(t, 0, 1)
		assert.Equal(t, throttleLoopDormant, throttle.loopState.Load())

		require.NoError(t, throttle.Attach(queue.controller))
		assert.Equal(t, throttleLoopActive, throttle.loopState.Load())
		require.NoError(t, throttle.Stop(context.Background()))
		waitThrottleValue(t, queue.resumeCalls)
		// цикл сворачивается сам, дожидаться его отдельным событием незачем
		synctest.Wait()
		assert.Equal(t, throttleLoopStopped, throttle.loopState.Load())

		throttle.Pause(context.Background(), time.Minute)
		require.NoError(t, throttle.Resume(context.Background()))
		require.NoError(t, throttle.Stop(context.Background()))
		require.ErrorContains(t, throttle.Attach(newPausedQueue(t, 0, 0).controller), "already stopped")
		requireNoThrottleValue(t, queue.pauseCalls)
		requireNoThrottleValue(t, queue.resumeCalls)
	})
}

func TestThrottleStopCompletionBroadcastsImmutableResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestThrottle_StopCancelsBlockedAutomaticResume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(block) })
		queue := newPausedQueue(t, 1, 2)
		queue.setResumeBlock(block)
		core, logs := observer.New(zap.ErrorLevel)
		throttle := newDeterministicThrottle(t, zap.New(core), queue)
		assert.Equal(t, throttleLoopActive, throttle.loopState.Load())

		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()
		time.Sleep(time.Minute)
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

		synctest.Wait()
		select {
		case err := <-stopDone:
			require.NoError(t, err)
		default:
			releaseOnce.Do(func() { close(block) })
			<-stopDone
			require.FailNow(t, "Stop не отменил заблокированный автоматический resume")
		}
		waitThrottleValue(t, pauseDone)
		synctest.Wait()
		assert.Equal(t, throttleLoopStopped, throttle.loopState.Load())
		assert.Zero(t, logs.FilterMessage("Не удалось автоматически вернуть опрос расчёта к работе").Len())
	})
}

func TestThrottle_StopClearsPersistedPauseAndIgnoresFuturePause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 1, 1)
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)
		throttle.Pause(context.Background(), time.Minute)
		waitThrottleValue(t, queue.pauseCalls)
		synctest.Wait()

		require.NoError(t, throttle.Stop(context.Background()))
		waitThrottleValue(t, queue.resumeCalls)
		time.Sleep(time.Hour)
		throttle.Pause(context.Background(), 2*time.Minute)
		require.NoError(t, throttle.Resume(context.Background()))

		requireNoThrottleValue(t, queue.pauseCalls)
		requireNoThrottleValue(t, queue.resumeCalls)
		paused, resumed := queue.counts()
		assert.Equal(t, 1, paused)
		assert.Equal(t, 1, resumed)
	})
}

func TestThrottle_StopTreatsMissingQueueAsClean(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 0, 1)
		queue.resumeErr = river.ErrNotFound
		core, logs := observer.New(zap.ErrorLevel)
		throttle := newDeterministicThrottle(t, zap.New(core), queue)

		require.NoError(t, throttle.Stop(context.Background()))
		assert.Zero(t, logs.Len())
	})
}

func TestThrottle_ConcurrentStopWaitsAndReturnsStoredResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resumeErr := errors.New("resume")
		release := make(chan struct{})
		queue := newPausedQueue(t, 0, 1)
		queue.resumeErr = resumeErr
		queue.resumeBlock = release
		throttle := newDeterministicThrottle(t, zap.NewNop(), queue)
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
	})
}

// Неподключённой очереди нечего придерживать: несостоявшийся Pause не
// оставляет ни срока, ни таймера, поэтому подключённая позже очередь
// не получит запоздалого вызова.
func TestThrottle_UnattachedPauseLeavesNoScheduledWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newPausedQueue(t, 0, 1)
		throttle := NewThrottle(zap.NewNop(), "orders")

		throttle.Pause(context.Background(), time.Minute)
		require.NoError(t, throttle.Attach(queue.controller))

		// забытый срок давно прошёл, а звать по нему некого
		time.Sleep(2 * time.Minute)
		requireNoThrottleValue(t, queue.pauseCalls)
		requireNoThrottleValue(t, queue.resumeCalls)

		require.NoError(t, throttle.Stop(context.Background()))
		waitThrottleValue(t, queue.resumeCalls)
	})
}

func TestThrottle_UnattachedStopIsHarmless(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		throttle := NewThrottle(zap.New(core), "orders")

		throttle.Pause(context.Background(), time.Minute)
		require.NoError(t, throttle.Stop(context.Background()))
		require.NoError(t, throttle.Stop(context.Background()))

		synctest.Wait()
		assert.Equal(t, throttleLoopStopped, throttle.loopState.Load())
		// без очереди паузе не за что зацепиться, и срок проходит впустую
		time.Sleep(time.Minute)
		throttle.Pause(context.Background(), time.Minute)
		require.NoError(t, throttle.Resume(context.Background()))
		assert.Zero(t, logs.Len())
	})
}

func TestThrottle_AttachReportsUnusableWiring(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		throttle := NewThrottle(zap.NewNop(), "orders")

		require.ErrorContains(t, throttle.Attach(nil), "nil queue controller")

		queue := newPausedQueue(t, 0, 1)
		require.NoError(t, throttle.Attach(queue.controller))
		require.ErrorContains(t, throttle.Attach(newPausedQueue(t, 0, 0).controller), "already attached")

		require.NoError(t, throttle.Stop(context.Background()))
		require.ErrorContains(t, throttle.Attach(newPausedQueue(t, 0, 0).controller), "already stopped")
		require.ErrorContains(t, throttle.Attach(nil), "nil queue controller")
	})
}
