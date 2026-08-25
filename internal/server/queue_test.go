package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

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

func TestBuildOrderDepsKeepsSuppliedRepositoryForLatePusher(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	storedOrders := orderrepository.New(gormDB)
	orderDeps := buildOrderDeps(storedOrders)
	pusher := &recordedPusher{}
	require.NoError(t, storedOrders.AttachPusher(pusher))

	err := orderDeps.Upload(context.Background(), "12345678903", 42)

	require.NoError(t, err)
	assert.Equal(t, []string{"12345678903"}, pusher.numbers)
	require.Len(t, pusher.transactions, 1)
	assert.NotNil(t, pusher.transactions[0])
}
