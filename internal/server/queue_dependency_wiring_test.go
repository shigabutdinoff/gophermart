package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestBuildDepsSharesOneOrderRepositoryBetweenHTTPAndWorkers(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	tokens, err := auth.NewJWTManager([]byte(testJWTSecret))
	require.NoError(t, err)
	var workerOrders domain.ResultWriter

	built, err := buildDeps(depsOptions{
		logger:         zap.NewNop(),
		queue:          newTestConfig().Queue,
		accrualAddress: testAccrualAddress,
		session:        session,
		tokens:         tokens,
		buildQueue: func(options queueOptions) (queueParts, error) {
			workerOrders = options.storedOrders

			return queueParts{}, nil
		},
	})
	require.NoError(t, err)
	storedOrders, ok := workerOrders.(*orderrepository.Repository)
	require.True(t, ok)
	pusher := &testkit.RecordingPusher{}
	require.NoError(t, storedOrders.AttachPusher(pusher))

	err = built.orders.Upload(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, 1, len(pusher.Numbers))
	assert.Equal(t, []string{"12345678903"}, pusher.Numbers)
	require.Len(t, pusher.Transactions, 1)
	// закрытая транзакция отвечает ErrTxDone: задание ушло именно той,
	// которую репозиторий закоммитил вместе с заказом
	assert.ErrorIs(t, pusher.Transactions[0].Rollback(), sql.ErrTxDone)
	assert.Equal(
		t,
		testkit.TransactionState{Begun: 1, Committed: 1},
		testkit.TransactionStateOf(t, gormDB),
	)
}

// Очередь получает ровно свои настройки: ни секрета, ни адреса сервиса,
// ни строки подключения в её зависимостях нет по типу.
func TestBuildDepsGivesQueueOnlyItsOwnSettings(t *testing.T) {
	tokens, err := auth.NewJWTManager([]byte(testJWTSecret))
	require.NoError(t, err)
	session, _ := testkit.NewDryRunSession(t)
	var passed queueOptions

	_, err = buildDeps(depsOptions{
		logger:         zap.NewNop(),
		queue:          newTestConfig().Queue,
		accrualAddress: testAccrualAddress,
		session:        session,
		tokens:         tokens,
		buildQueue: func(options queueOptions) (queueParts, error) {
			passed = options

			return queueParts{}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, testAccrualAddress, passed.accrualAddress)
	assert.Equal(t, config.Default().Queue, passed.queue)
}
