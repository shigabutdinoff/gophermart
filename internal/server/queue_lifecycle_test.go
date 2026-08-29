package server

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestQueueActorStartsRunnerAfterDatabaseReadiness(t *testing.T) {
	lifecycle := newQueueActorRiverLifecycle()
	runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
	// цикл runner заводится в конструкторе и должен уйти до конца пузыря
	defer func() {
		lifecycle.closeStopped()
		_ = runner.Stop(context.Background())
	}()
	s := &Server{
		logger: zap.NewNop(),
		deps:   deps{runner: runner},
	}
	ready := make(chan struct{})
	actor, interrupt := s.queueActor(context.Background(), ready)
	done := make(chan error, 1)
	go func() { done <- actor() }()

	close(ready)
	<-lifecycle.startCalled
	<-lifecycle.stoppedCalled

	interrupt(nil)
	require.NoError(t, <-done)
	assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	assert.Equal(t, 2, countCalls(lifecycle.callLog(), "stopped"))
}

func TestQueueActorInterruptWhileWaitingForReadinessSkipsRunnerStart(t *testing.T) {
	lifecycle := newQueueActorRiverLifecycle()
	runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
	// цикл runner заводится в конструкторе и должен уйти до конца пузыря
	defer func() {
		lifecycle.closeStopped()
		_ = runner.Stop(context.Background())
	}()
	s := &Server{
		logger: zap.NewNop(),
		deps:   deps{runner: runner},
	}
	ready := make(chan struct{})
	actor, interrupt := s.queueActor(context.Background(), ready)
	done := make(chan error, 1)
	go func() { done <- actor() }()

	interrupt(nil)
	require.NoError(t, <-done)
	assert.Empty(t, lifecycle.callLog())
}

func TestQueueActorRetainsContextValuesAndInterruptionDoesNotStopRiver(t *testing.T) {
	lifecycle := newQueueActorRiverLifecycle()
	runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
	// цикл runner заводится в конструкторе и должен уйти до конца пузыря
	defer func() {
		lifecycle.closeStopped()
		_ = runner.Stop(context.Background())
	}()
	s := &Server{
		logger: zap.NewNop(),
		deps:   deps{runner: runner},
	}
	signalCtx, cancel := context.WithCancel(
		context.WithValue(context.Background(), queueActorContextKey{}, "kept"),
	)
	actor, interrupt := s.queueActor(signalCtx, closedDatabaseReadiness())
	done := make(chan error, 1)
	go func() { done <- actor() }()
	<-lifecycle.stoppedCalled

	cancel()
	interrupt(nil)
	require.NoError(t, <-done)
	lifecycle.mu.Lock()
	assert.Equal(t, "kept", lifecycle.startCtx.Value(queueActorContextKey{}))
	assert.NoError(t, lifecycle.startCtx.Err())
	lifecycle.mu.Unlock()
	assert.NotContains(t, lifecycle.callLog(), "stop")
	assert.NotContains(t, lifecycle.callLog(), "stop_and_cancel")
}

func TestQueueActorRaisesQueueAfterUnexpectedStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.ErrorLevel)
		lifecycle := newQueueActorRiverLifecycle()
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.New(core),
			retryDelay: time.Second,
			deps:       deps{runner: runner},
		}
		actor, interrupt := s.queueActor(context.Background(), closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()
		<-lifecycle.stoppedCalled

		// сама очередь не восстановится, а без опроса заказы ждали бы расчёта
		// до перезапуска процесса
		lifecycle.closeStopped()
		synctest.Wait()
		assert.Equal(t, 1, logs.FilterMessage("Очередь заданий неожиданно остановилась").Len())
		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		synctest.Wait()
		assert.GreaterOrEqual(t, countCalls(lifecycle.callLog(), "start"), 2)
		select {
		case err := <-done:
			require.NoError(t, err)
			require.FailNow(t, "actor вернулся после неожиданной остановки River")
		default:
		}

		interrupt(nil)
		require.NoError(t, <-done)
	})
}

// Прерывание застаёт паузу между подъёмами: поднимать очередь уже незачем.
func TestQueueActorStopsRestartingWhenInterrupted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newQueueActorRiverLifecycle()
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.NewNop(),
			retryDelay: time.Hour,
			deps:       deps{runner: runner},
		}
		actor, interrupt := s.queueActor(context.Background(), closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()
		<-lifecycle.stoppedCalled

		lifecycle.closeStopped()
		synctest.Wait()

		interrupt(nil)
		require.NoError(t, <-done)
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
	})
}

func TestServer_RepeatsQueueStartUntilItSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		lifecycle := newQueueActorRiverLifecycle()
		lifecycle.startFailures = 2
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.New(core),
			retryDelay: time.Millisecond,
			deps:       deps{runner: runner},
		}
		ctx, cancel := context.WithCancel(context.Background())
		actor, interrupt := s.queueActor(ctx, closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()
		synctest.Wait()
		time.Sleep(time.Millisecond - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, 1, countCalls(lifecycle.callLog(), "start"))
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		synctest.Wait()
		time.Sleep(2*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, 2, countCalls(lifecycle.callLog(), "start"))
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		<-lifecycle.stoppedCalled

		cancel()
		interrupt(nil)
		require.NoError(t, <-done)
		assert.Equal(t, 2, logs.FilterMessage("Повторная попытка запустить очередь заданий").Len())
		assert.Equal(t, 3, countCalls(lifecycle.callLog(), "start"))
	})
}

func TestServer_QueueStartBackoffUsesRetryDelayAndCapsAtThirtySeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantDelays := []time.Duration{
			time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
			16 * time.Second,
			retryMaxDelay,
			retryMaxDelay,
		}
		lifecycle := newQueueActorRiverLifecycle()
		lifecycle.startFailures = len(wantDelays)
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.NewNop(),
			retryDelay: time.Second,
			deps:       deps{runner: runner},
		}

		done := make(chan error, 1)
		go func() { done <- s.startQueue(context.Background()) }()
		for index, delay := range wantDelays {
			synctest.Wait()
			time.Sleep(delay - time.Nanosecond)
			synctest.Wait()
			assert.Equal(t, index+1, countCalls(lifecycle.callLog(), "start"))
			time.Sleep(time.Nanosecond)
			synctest.Wait()
		}

		require.NoError(t, <-done)
		assert.Equal(t, len(wantDelays)+1, countCalls(lifecycle.callLog(), "start"))
	})
}
