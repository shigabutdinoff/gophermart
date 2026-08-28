package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/jobs"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
	servermocks "github.com/shigabutdinoff/gophermart/internal/server/mocks"
)

type observedQueueThrottle struct {
	attachments int
	attachErr   error
}

func (t *observedQueueThrottle) attach(jobs.QueueController) error {
	t.attachments++

	return t.attachErr
}

func (t *observedQueueThrottle) mock(testingT interface {
	mock.TestingT
	Cleanup(func())
}) *servermocks.MockQueueThrottle {
	throttle := servermocks.NewMockQueueThrottle(testingT)
	throttle.EXPECT().Attach(mock.Anything).RunAndReturn(t.attach).Once()

	return throttle
}

func TestNewQueueClient_WithoutDatabaseStaysEmpty(t *testing.T) {
	queue, err := newQueueClient(queueOptions{
		logger:       zap.NewNop(),
		cfg:          newTestConfig(),
		now:          time.Now,
		storedOrders: orderrepository.New(nil),
		sqlDB:        nil,
	})

	require.NoError(t, err)
	assert.Nil(t, queue.client)
	assert.Nil(t, queue.runner)
}

func TestNewQueueClient_UnusableAccrualAddressLeavesQueueForDispatchOnly(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	cfg := newTestConfig()
	cfg.AccrualAddress = ""
	gormDB, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:       zap.New(core),
		cfg:          cfg,
		now:          time.Now,
		storedOrders: orderrepository.New(gormDB),
		sqlDB:        sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client, "заказы ставятся в очередь даже без опроса расчёта")
	assert.Nil(t, queue.runner)
	assert.Equal(t, 1, logs.FilterMessage("Опрос системы расчёта отключён").Len())
}

func TestNewQueueClient_SyntacticallyInvalidAccrualAddressHasNoWorkerController(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	cfg := newTestConfig()
	cfg.AccrualAddress = "htp://localhost:8080"
	gormDB, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:       zap.New(core),
		cfg:          cfg,
		now:          time.Now,
		storedOrders: orderrepository.New(gormDB),
		sqlDB:        sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client, "dispatch-only client remains available")
	assert.Nil(t, queue.runner, "without a runner throttle is never attached and starts no controller")
	assert.Equal(t, 1, logs.FilterMessage("Опрос системы расчёта отключён").Len())
}

func TestNewQueueClient_UsableAccrualAddressGivesWorkingQueue(t *testing.T) {
	gormDB, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:       zap.NewNop(),
		cfg:          newTestConfig(),
		now:          time.Now,
		storedOrders: orderrepository.New(gormDB),
		sqlDB:        sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client)
	assert.NotNil(t, queue.runner, "очередь с воркерами получает runner")
}

func TestNewQueueClient_RunnableConstructionOnlyAttachesThrottle(t *testing.T) {
	gormDB, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })
	throttle := &observedQueueThrottle{}

	queue, err := newQueueClient(queueOptions{
		logger:       zap.NewNop(),
		cfg:          newTestConfig(),
		now:          time.Now,
		storedOrders: orderrepository.New(gormDB),
		sqlDB:        sqlDB,
		newThrottle: func(*zap.Logger, string, auth.Clock) queueThrottle {
			return throttle.mock(t)
		},
	})

	require.NoError(t, err)
	require.NotNil(t, queue.runner)
	assert.Equal(t, 1, throttle.attachments)
}
