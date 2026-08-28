package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
)

func TestNewQueueClient_ReportsUnusableQueueSettings(t *testing.T) {
	cfg := newTestConfig()
	// очередь не берёт задания чаще, чем раз в FetchCooldown
	cfg.Queue.FetchPollInterval = time.Millisecond
	session, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err := newQueueClient(queueOptions{
		logger:          zap.NewNop(),
		queue:           cfg.Queue,
		accrualAddress:  cfg.AccrualAddress,
		storedOrders:    orderrepository.New(session),
		sqlDB:           sqlDB,
		shutdownTimeout: DefaultShutdownTimeout,
	})

	require.Error(t, err, "иначе загрузка заказов молча отвечала бы отказом")
	assert.ErrorContains(t, err, "start job queue")
}

func TestNew_FailsOnUnusableQueueSettings(t *testing.T) {
	cfg := newTestConfig()
	cfg.DatabaseURI = unavailableDatabaseDSN
	cfg.Queue.FetchPollInterval = time.Millisecond

	_, err := New(zap.NewNop(), cfg)

	require.Error(t, err)
}
