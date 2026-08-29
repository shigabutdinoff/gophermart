package server

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestQueueActorResumesPendingOrdersAfterEachSuccessfulStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)
		lifecycle := newQueueActorRiverLifecycle()
		resumer := &queueActorPendingOrderResumer{inserted: 1}
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.New(core),
			retryDelay: time.Second,
			deps: deps{
				runner:              runner,
				pendingOrderResumer: resumer.mock(t),
			},
		}
		actor, interrupt := s.queueActor(context.Background(), closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()

		synctest.Wait()
		require.True(t, resumer.count() == 1)
		lifecycle.closeStopped()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		synctest.Wait()
		require.True(t, resumer.count() == 2)

		interrupt(nil)
		require.NoError(t, <-done)
		assert.Equal(t, 2, logs.FilterMessage("Незакрытые заказы возвращены в очередь").Len())
		for _, entry := range logs.FilterMessage("Незакрытые заказы возвращены в очередь").All() {
			assert.Equal(t, int64(1), entry.ContextMap()["count"])
		}
	})
}

func TestQueueActorResumesOnlyAfterEventualSuccessfulQueueStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newQueueActorRiverLifecycle()
		lifecycle.startFailures = 2
		resumer := &queueActorPendingOrderResumer{}
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.NewNop(),
			retryDelay: time.Second,
			deps: deps{
				runner:              runner,
				pendingOrderResumer: resumer.mock(t),
			},
		}
		actor, interrupt := s.queueActor(context.Background(), closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()

		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		synctest.Wait()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		synctest.Wait()
		require.True(t, resumer.count() == 1)
		interrupt(nil)
		require.NoError(t, <-done)
		assert.Equal(t, 3, countCalls(lifecycle.callLog(), "start"))
		assert.Equal(t, 1, resumer.count())
	})
}

func TestQueueActorDoesNotResumeWhenInterruptedBeforeQueueStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := newQueueActorRiverLifecycle()
		lifecycle.startFailures = 1
		resumer := &queueActorPendingOrderResumer{}
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger:     zap.NewNop(),
			retryDelay: time.Hour,
			deps: deps{
				runner:              runner,
				pendingOrderResumer: resumer.mock(t),
			},
		}
		actor, interrupt := s.queueActor(context.Background(), closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()

		synctest.Wait()
		interrupt(nil)
		require.NoError(t, <-done)
		assert.Zero(t, resumer.count())
	})
}

func TestQueueActorWarnsOncePerRecoveryAttemptWhenPendingOrdersCannotBeResumed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core, logs := observer.New(zap.DebugLevel)
		lifecycle := newQueueActorRiverLifecycle()
		resumeErr := errors.New("resume pending orders")
		resumer := &queueActorPendingOrderResumer{inserted: 1, err: resumeErr}
		runner := newRiverRunner(lifecycle.mock(t), newQueueActorThrottle(t))
		// цикл runner заводится в конструкторе и должен уйти до конца пузыря
		defer func() {
			lifecycle.closeStopped()
			_ = runner.Stop(context.Background())
		}()
		s := &Server{
			logger: zap.New(core),
			deps: deps{
				runner:              runner,
				pendingOrderResumer: resumer.mock(t),
			},
		}
		actor, interrupt := s.queueActor(context.Background(), closedDatabaseReadiness())
		done := make(chan error, 1)
		go func() { done <- actor() }()

		require.Eventually(t, func() bool {
			return logs.FilterMessage("Не удалось вернуть незакрытые заказы в очередь").Len() == 1
		}, time.Second, time.Millisecond)
		select {
		case err := <-done:
			require.NoError(t, err)
			require.FailNow(t, "actor завершился после ошибки восстановления")
		default:
		}

		lifecycle.closeStopped()
		synctest.Wait()
		require.True(t, resumer.count() == 2)
		require.Eventually(t, func() bool {
			return logs.FilterMessage("Не удалось вернуть незакрытые заказы в очередь").Len() == 2
		}, time.Second, time.Millisecond)
		for _, entry := range logs.FilterMessage("Не удалось вернуть незакрытые заказы в очередь").All() {
			assert.Equal(t, int64(1), entry.ContextMap()["count"])
			assert.Equal(t, resumeErr.Error(), entry.ContextMap()["error"])
		}
		assert.Zero(t, logs.FilterMessage("Незакрытые заказы возвращены в очередь").Len())

		interrupt(nil)
		require.NoError(t, <-done)
	})
}

func TestServerResumePendingOrdersDoesNothingWithoutQueue(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	s := &Server{
		logger: zap.New(core),
	}

	s.resumePendingOrders(context.Background())

	assert.Empty(t, logs.All())
}

func TestServerResumePendingOrdersLogsSuccessfulZeroInsert(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	resumer := &queueActorPendingOrderResumer{}
	s := &Server{
		logger: zap.New(core),
		deps: deps{
			pendingOrderResumer: resumer.mock(t),
		},
	}

	s.resumePendingOrders(context.Background())

	entries := logs.FilterMessage("Незакрытые заказы возвращены в очередь").All()
	require.Len(t, entries, 1)
	assert.Equal(t, int64(0), entries[0].ContextMap()["count"])
}
