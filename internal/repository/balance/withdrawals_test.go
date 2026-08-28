package balance

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_ListWithdrawalsReadsUserHistoryNewestFirst(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "withdrawals")
	processedAt := time.Date(2026, time.August, 28, 12, 34, 56, 0, time.UTC)
	var operationContext context.Context
	var query string
	var variables []any
	require.NoError(t, gormDB.Callback().Query().After("testkit:dry-run-query-result").Register(
		"test:observe-withdrawals",
		func(tx *gorm.DB) {
			operationContext = tx.Statement.Context
			query = tx.Statement.SQL.String()
			variables = slices.Clone(tx.Statement.Vars)
		},
	))
	testkit.SetQueryResult(t, gormDB, []withdrawalRow{
		{Order: "2377225624", Sum: money.Points(75150), ProcessedAt: processedAt},
	}, nil)

	got, err := New(gormDB).ListWithdrawals(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, []domain.Withdrawal{
		{Order: "2377225624", Sum: money.Points(75150), ProcessedAt: processedAt},
	}, got)
	assert.Same(t, ctx, operationContext)
	assert.Contains(t, query, `SELECT order_number, sum, processed_at FROM "withdrawals"`)
	assert.Contains(t, query, `WHERE user_id = $1`)
	assert.Contains(t, query, `ORDER BY processed_at DESC, id DESC`)
	assert.Equal(t, []any{int64(42)}, variables)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_ListWithdrawalsReturnsDatabaseError(t *testing.T) {
	storageErr := errors.New("storage")
	gormDB := testkit.NewDryRunDB(t)
	testkit.SetQueryResult(t, gormDB, []withdrawalRow(nil), storageErr)

	_, err := New(gormDB).ListWithdrawals(context.Background(), 42)

	require.ErrorIs(t, err, storageErr)
}
