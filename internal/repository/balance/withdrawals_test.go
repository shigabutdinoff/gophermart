package balance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_ListWithdrawalsReadsUserHistoryNewestFirst(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "withdrawals")
	processedAt := time.Date(2026, time.August, 28, 12, 34, 56, 0, time.UTC)
	observed := testkit.ObserveStatements(t, gormDB)
	testkit.SetQueryResult(gormDB, []withdrawalRow{
		{Order: "2377225624", Sum: money.Points(75150), ProcessedAt: processedAt},
	}, nil)

	got, err := New(session).ListWithdrawals(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, []domain.Withdrawal{
		{Order: "2377225624", Sum: money.Points(75150), ProcessedAt: processedAt},
	}, got)
	statement := observed()
	assert.Same(t, ctx, statement.Context)
	assert.Contains(t, statement.SQL, `SELECT order_number, sum, processed_at FROM "withdrawals"`)
	assert.Contains(t, statement.SQL, `WHERE user_id = $1`)
	assert.Contains(t, statement.SQL, `ORDER BY processed_at DESC, id DESC`)
	assert.Equal(t, []any{int64(42)}, statement.Variables)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_ListWithdrawalsReturnsDatabaseError(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetQueryResult(gormDB, []withdrawalRow(nil), storageErr)

	_, err := New(session).ListWithdrawals(context.Background(), 42)

	require.ErrorIs(t, err, storageErr)
}
