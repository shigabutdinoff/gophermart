package balance

import (
	"context"
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
	session, gormDB := testkit.NewDryRunSession(t)
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
	testkit.SetQueryResult(gormDB, []balanceRow{{
		Current:   money.Points(50050),
		Withdrawn: money.Points(4200),
	}}, nil)

	got, err := New(session).Balance(ctx, 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Balance{
		Current:   money.Points(50050),
		Withdrawn: money.Points(4200),
	}, got)
	assert.Equal(t, 1, queries)
	assert.Same(t, ctx, operationContext)
	query = strings.Join(strings.Fields(query), " ")
	assert.Contains(t, query, `SELECT accrued - withdrawn AS current, withdrawn`)
	assert.Contains(t, query, `FROM ( SELECT`)
	assert.Contains(t, query, `(SELECT COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0) FROM orders WHERE user_id = $1) AS accrued`)
	assert.Contains(t, query, `(SELECT COALESCE(SUM(sum), 0) FROM withdrawals WHERE user_id = $2) AS withdrawn`)
	assert.Contains(t, query, `AS balance_totals`)
	assert.Contains(t, query, `LIMIT $3`)
	assert.NotContains(t, query, "GROUP BY")
	assert.Equal(t, []any{int64(42), int64(42), 1}, variables)
}

func TestRepository_BalanceReturnsZeroForEmptyAccount(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetQueryResult(gormDB, []balanceRow{{}}, nil)

	got, err := New(session).Balance(context.Background(), 42)

	require.NoError(t, err)
	assert.Equal(t, domain.Balance{}, got)
}

func TestRepository_BalanceWithoutDatabaseIsControlled(t *testing.T) {
	_, err := New(database.Session{}).Balance(context.Background(), 42)

	require.ErrorIs(t, err, database.ErrUnavailable)
}
