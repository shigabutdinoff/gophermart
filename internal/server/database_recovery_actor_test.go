package server

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

func TestDatabaseRecoveryActor_RetriesFullPreparationUntilReadyAndWaitsForInterrupt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pingErr := errors.New("ping failed")
		migrationErr := errors.New("migration failed")
		events := &databaseEventLog{}
		db, connector := newScriptedPingDatabase(t, func(call int32) error {
			events.add("ping")
			if call <= databaseConnectAttempts {
				return pingErr
			}
			return nil
		})
		core, logs := observer.New(zap.WarnLevel)
		s := &Server{
			logger:     zap.New(core),
			retryDelay: time.Millisecond,
			sqlDB:      db,
		}
		ready := make(chan struct{})
		var migrationCalls atomic.Int32
		actor, interrupt := s.databaseRecoveryActor(
			context.Background(),
			ready,
			func(context.Context, *sql.DB) error {
				events.add("migrate")
				if migrationCalls.Add(1) == 1 {
					return migrationErr
				}
				return nil
			},
		)
		require.NotNil(t, actor)
		require.NotNil(t, interrupt)
		done := make(chan error, 1)
		go func() { done <- actor() }()

		// внутренний retry успевает исчерпать бюджет попыток подключения
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		events.add("wait")
		assert.Equal(t, int32(databaseConnectAttempts), connector.calls.Load())
		time.Sleep(time.Millisecond - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, int32(databaseConnectAttempts), connector.calls.Load())
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		events.add("wait")
		assert.Equal(t, int32(1), migrationCalls.Load())
		time.Sleep(2*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, int32(1), migrationCalls.Load())
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		<-ready
		assert.Equal(t, int32(2), migrationCalls.Load())
		assert.Equal(
			t,
			[]string{"ping", "ping", "ping", "wait", "ping", "migrate", "wait", "ping", "migrate"},
			events.snapshot(),
		)
		assert.Equal(t, 2, logs.FilterMessage("Повторная попытка подготовить БД").Len())
		select {
		case <-done:
			require.FailNow(t, "actor завершился после readiness")
		default:
		}

		interrupt(nil)
		require.NoError(t, <-done)
	})
}

func TestDatabaseRecoveryActor_CancellationStopsRetriesPromptly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, connector := newScriptedPingDatabase(t, func(int32) error {
			return database.ErrUnavailable
		})
		s := &Server{
			logger:     zap.NewNop(),
			retryDelay: time.Millisecond,
			sqlDB:      db,
		}
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan struct{})
		migrationCalls := atomic.Int32{}
		actor, _ := s.databaseRecoveryActor(ctx, ready, func(context.Context, *sql.DB) error {
			migrationCalls.Add(1)
			return nil
		})
		done := make(chan error, 1)
		go func() { done <- actor() }()

		synctest.Wait()
		cancel()
		require.NoError(t, <-done)
		assert.NotZero(t, connector.calls.Load(), "первая попытка идёт без паузы")
		assert.Zero(t, migrationCalls.Load())
		select {
		case <-ready:
			require.FailNow(t, "отмена не должна открывать readiness")
		default:
		}
	})
}

func TestDatabaseLifecycle_SynchronousSuccessIsReadyWithoutRecoveryActor(t *testing.T) {
	events := &databaseEventLog{}
	db, _ := newScriptedPingDatabase(t, func(int32) error {
		events.add("ping")
		return nil
	})
	s := &Server{logger: zap.NewNop(), retryDelay: time.Millisecond, sqlDB: db}

	ready, actor, interrupt := s.databaseLifecycle(context.Background(), func(context.Context, *sql.DB) error {
		events.add("migrate")
		return nil
	})

	receiveWithin(t, ready)
	assert.Nil(t, actor)
	assert.Nil(t, interrupt)
	assert.Equal(t, []string{"ping", "migrate"}, events.snapshot())
}

func TestDatabaseLifecycle_WithoutDatabaseHasNoRecoveryActor(t *testing.T) {
	s := &Server{logger: zap.NewNop(), retryDelay: time.Millisecond}

	ready, actor, interrupt := s.databaseLifecycle(context.Background(), func(context.Context, *sql.DB) error {
		require.FailNow(t, "миграции без sql.DB не вызываются")
		return nil
	})

	assert.Nil(t, actor)
	assert.Nil(t, interrupt)
	select {
	case <-ready:
		require.FailNow(t, "отсутствующая БД не готова")
	default:
	}
}
