package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

type recordedPusher struct {
	transactions []*gorm.DB
	numbers      []string
}

func (p *recordedPusher) Push(_ context.Context, tx *gorm.DB, number string) error {
	p.transactions = append(p.transactions, tx)
	p.numbers = append(p.numbers, number)

	return nil
}

func TestBuildDepsSharesOneOrderRepositoryBetweenHTTPAndWorkers(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	tokens, err := auth.NewJWTManager([]byte(testJWTSecret), time.Now)
	require.NoError(t, err)
	var workerOrders domain.ResultWriter

	built, err := buildDeps(depsOptions{
		logger: zap.NewNop(),
		cfg:    newTestConfig(),
		gormDB: gormDB,
		tokens: tokens,
		buildQueue: func(options queueOptions) (queueParts, error) {
			workerOrders = options.storedOrders

			return queueParts{}, nil
		},
	})
	require.NoError(t, err)
	storedOrders, ok := workerOrders.(*orderrepository.Repository)
	require.True(t, ok)
	pusher := &recordedPusher{}
	require.NoError(t, storedOrders.AttachPusher(pusher))

	err = built.orders.Upload(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, []string{"12345678903"}, pusher.numbers)
	require.Len(t, pusher.transactions, 1)
	assert.NotNil(t, pusher.transactions[0])
}

func TestNewQueueClient_WithoutDatabaseStaysEmpty(t *testing.T) {
	queue, err := newQueueClient(queueOptions{
		logger:       zap.NewNop(),
		cfg:          newTestConfig(),
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
		storedOrders: orderrepository.New(gormDB),
		sqlDB:        sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client, "заказы ставятся в очередь даже без опроса расчёта")
	assert.Nil(t, queue.runner)
	assert.Equal(t, 1, logs.FilterMessage("Опрос системы расчёта отключён").Len())
}

func TestNewQueueClient_UsableAccrualAddressGivesWorkingQueue(t *testing.T) {
	gormDB, sqlDB := openDatabase(zap.NewNop(), unavailableDatabaseDSN)
	require.NotNil(t, sqlDB)
	t.Cleanup(func() { _ = sqlDB.Close() })

	queue, err := newQueueClient(queueOptions{
		logger:       zap.NewNop(),
		cfg:          newTestConfig(),
		storedOrders: orderrepository.New(gormDB),
		sqlDB:        sqlDB,
	})

	require.NoError(t, err)
	require.NotNil(t, queue.client)
	assert.NotNil(t, queue.runner, "очередь с воркерами получает runner")
}
