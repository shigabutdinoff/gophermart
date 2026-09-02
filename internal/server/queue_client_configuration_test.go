package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
)

func TestNewQueueClient_WithoutDatabaseStaysEmpty(t *testing.T) {
	queue, err := newQueueClient(queueOptions{
		logger:         zap.NewNop(),
		queue:          newTestConfig().Queue,
		accrualAddress: testAccrualAddress,
		storedOrders:   orderrepository.New(database.Session{}),
		sqlDB:          nil,
	})

	require.NoError(t, err)
	assert.Nil(t, queue.client)
	assert.Nil(t, queue.runner)
}

func TestNewQueueClient_UnusableAccrualAddressLeavesQueueForDispatchOnly(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	session, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:         zap.New(core),
		queue:          newTestConfig().Queue,
		accrualAddress: "",
		storedOrders:   orderrepository.New(session),
		sqlDB:          sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client, "заказы ставятся в очередь даже без опроса расчёта")
	assert.Nil(t, queue.runner)
	assert.Equal(t, 1, logs.FilterMessage("Опрос системы расчёта отключён").Len())
}

func TestNewQueueClient_SyntacticallyInvalidAccrualAddressHasNoWorkerController(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	session, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:         zap.New(core),
		queue:          newTestConfig().Queue,
		accrualAddress: "htp://localhost:8080",
		storedOrders:   orderrepository.New(session),
		sqlDB:          sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client, "dispatch-only client remains available")
	assert.Nil(t, queue.runner, "without a runner throttle is never attached and starts no controller")
	assert.Equal(t, 1, logs.FilterMessage("Опрос системы расчёта отключён").Len())
}

func TestNewQueueClient_UsableAccrualAddressGivesWorkingQueue(t *testing.T) {
	session, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:         zap.NewNop(),
		queue:          newTestConfig().Queue,
		accrualAddress: testAccrualAddress,
		storedOrders:   orderrepository.New(session),
		sqlDB:          sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client)
	// Attach отдаёт ошибку наружу, поэтому runner доказывает и его успех
	assert.NotNil(t, queue.runner, "очередь с воркерами получает runner")
}
