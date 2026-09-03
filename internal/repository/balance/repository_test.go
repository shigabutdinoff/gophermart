package balance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_BalanceReadsBothTotalsInOneQuery(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "value")
	observed := testkit.ObserveStatements(t, gormDB)
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
	statement := observed()
	assert.Equal(t, 1, statement.Calls)
	assert.Same(t, ctx, statement.Context)
	assert.Contains(t, statement.SQL, `FROM "orders"`)
	assert.Contains(t, statement.SQL, `COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0)`)
	assert.Equal(t, 2, strings.Count(statement.SQL, "FROM withdrawals WHERE user_id ="))
	assert.Equal(t, 2, strings.Count(statement.SQL, "COALESCE(SUM(sum), 0)"))
	assert.Contains(t, statement.SQL, `WHERE user_id = $3`)
	assert.Contains(t, statement.SQL, `LIMIT $4`)
	assert.NotContains(t, statement.SQL, "GROUP BY")
	assert.Equal(t, []any{int64(42), int64(42), int64(42), 1}, statement.Variables)
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

func TestRepository_WithdrawConditionallyInsertsByAvailableBalance(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "withdraw")
	observed := testkit.ObserveStatements(t, gormDB)
	testkit.SetExecResult(gormDB, 1, nil)

	outcome, err := New(session).
		Withdraw(ctx, 42, "2377225624", money.Points(75100))

	require.NoError(t, err)
	assert.Equal(t, domain.Withdrawn, outcome)
	statement := observed()
	query := strings.Join(strings.Fields(statement.SQL), " ")
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
	}, statement.Variables)
	assert.Same(t, ctx, statement.Context)
}

func TestRepository_WithdrawReportsInsufficientBalance(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetExecResult(gormDB, 0, nil)

	outcome, err := New(session).
		Withdraw(context.Background(), 42, "2377225624", 100)

	require.NoError(t, err)
	assert.Equal(t, domain.NotEnoughFunds, outcome)
}

func TestRepository_WithdrawReportsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetExecResult(gormDB, 0, storageErr)

	_, err := New(session).Withdraw(context.Background(), 42, "2377225624", 100)

	require.ErrorIs(t, err, storageErr)
	assert.ErrorContains(t, err, "insert withdrawal")
}

func TestRepository_WithdrawWithoutDatabaseIsControlled(t *testing.T) {
	_, err := New(database.Session{}).Withdraw(context.Background(), 42, "2377225624", 100)

	require.ErrorIs(t, err, database.ErrUnavailable)
}

type repositoryContextKey struct{}
