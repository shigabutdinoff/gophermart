package balance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_BalanceReadsBothTotalsInOneQuery(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	queries := 0
	var operationContext context.Context
	var query string
	var variables []any
	require.NoError(t, gormDB.Callback().Query().After("testkit:dry-run-query-result").Register(
		"test:observe-balance",
		func(tx *gorm.DB) {
			queries++
			operationContext = tx.Statement.Context
			query = tx.Statement.SQL.String()
			variables = slices.Clone(tx.Statement.Vars)
		},
	))
	testkit.SetQueryResult(t, gormDB, []balanceRow{{
		Current:   money.Points(50050),
		Withdrawn: money.Points(4200),
	}}, nil)

	got, err := New(gormDB).Balance(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Balance{
		Current:   money.Points(50050),
		Withdrawn: money.Points(4200),
	}, got)
	assert.Equal(t, 1, queries)
	assert.Same(t, ctx, operationContext)
	assert.Contains(t, query, `FROM "orders"`)
	assert.Contains(t, query, `COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0)`)
	assert.Equal(t, 2, strings.Count(query, "FROM withdrawals WHERE user_id ="))
	assert.Equal(t, 2, strings.Count(query, "COALESCE(SUM(sum), 0)"))
	assert.Contains(t, query, `WHERE user_id = $3`)
	assert.Contains(t, query, `LIMIT $4`)
	assert.NotContains(t, query, "GROUP BY")
	assert.Equal(t, []any{int64(42), int64(42), int64(42), 1}, variables)
}

func TestRepository_BalanceReturnsZeroForEmptyAccount(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	testkit.SetQueryResult(t, gormDB, []balanceRow{{}}, nil)

	got, err := New(gormDB).Balance(context.Background(), 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Balance{}, got)
}

func TestRepository_BalanceWithoutDatabaseIsControlled(t *testing.T) {
	_, err := New(nil).Balance(context.Background(), 42)

	require.ErrorIs(t, err, database.ErrUnavailable)
}

func TestRepository_WithdrawConditionallyInsertsByAvailableBalance(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "withdraw")
	var query string
	var variables []any
	var operationContext context.Context
	require.NoError(t, gormDB.Callback().Raw().After("testkit:dry-run-exec-result").Register(
		"test:observe-withdraw",
		func(tx *gorm.DB) {
			query = strings.Join(strings.Fields(tx.Statement.SQL.String()), " ")
			variables = slices.Clone(tx.Statement.Vars)
			operationContext = tx.Statement.Context
		},
	))
	testkit.SetExecResult(t, gormDB, 1, nil)

	outcome, err := New(gormDB).Withdraw(ctx, 42, "2377225624", money.Points(75100))

	require.NoError(t, err)
	assert.Equal(t, domain.Withdrawn, outcome)
	assert.Contains(t, query, `INSERT INTO withdrawals (user_id, order_number, sum) SELECT $1, $2, $3`)
	assert.Contains(t, query, `FROM orders WHERE user_id = $4`)
	assert.Contains(t, query, `FROM withdrawals WHERE user_id = $5`)
	assert.Contains(t, query, `>= $6`)
	assert.NotContains(t, query, "pg_advisory_xact_lock")
	assert.NotContains(t, query, "ON CONFLICT")
	assert.Equal(t, []any{
		int64(42),
		"2377225624",
		money.Points(75100),
		int64(42),
		int64(42),
		money.Points(75100),
	}, variables)
	assert.Same(t, ctx, operationContext)
}

func TestRepository_WithdrawReportsInsufficientBalance(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	testkit.SetExecResult(t, gormDB, 0, nil)

	outcome, err := New(gormDB).Withdraw(context.Background(), 42, "2377225624", 100)

	require.NoError(t, err)
	assert.Equal(t, domain.NotEnoughFunds, outcome)
}

func TestRepository_WithdrawReportsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage")
	gormDB := testkit.NewDryRunDB(t)
	testkit.SetExecResult(t, gormDB, 0, storageErr)

	_, err := New(gormDB).Withdraw(context.Background(), 42, "2377225624", 100)

	require.ErrorIs(t, err, storageErr)
	assert.ErrorContains(t, err, "insert withdrawal")
}

func TestRepository_WithdrawWithoutDatabaseIsControlled(t *testing.T) {
	_, err := New(nil).Withdraw(context.Background(), 42, "2377225624", 100)

	require.ErrorIs(t, err, database.ErrUnavailable)
}

type repositoryContextKey struct{}
