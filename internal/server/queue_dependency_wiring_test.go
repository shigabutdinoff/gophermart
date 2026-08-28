package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
	ordermocks "github.com/shigabutdinoff/gophermart/internal/repository/order/mocks"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

type pusherState struct {
	transactions []*gorm.DB
	numbers      []string
}

func (s *pusherState) mock(t *testing.T) *ordermocks.MockPusher {
	t.Helper()

	pusher := ordermocks.NewMockPusher(t)
	pusher.EXPECT().Push(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, tx *gorm.DB, number string) {
			s.transactions = append(s.transactions, tx)
			s.numbers = append(s.numbers, number)
		}).
		Return(nil).
		Once()

	return pusher
}

func TestBuildDepsSharesOneOrderRepositoryBetweenHTTPAndWorkers(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	tokens, err := auth.NewJWTManager([]byte(testJWTSecret), time.Now)
	require.NoError(t, err)
	var workerOrders domain.ResultWriter

	built, err := buildDeps(depsOptions{
		logger: zap.NewNop(),
		cfg:    newTestConfig(),
		now:    time.Now,
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
	pusher := &pusherState{}
	require.NoError(t, storedOrders.AttachPusher(pusher.mock(t)))

	err = built.orders.Upload(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, []string{"12345678903"}, pusher.numbers)
	require.Len(t, pusher.transactions, 1)
	assert.NotNil(t, pusher.transactions[0])
}
