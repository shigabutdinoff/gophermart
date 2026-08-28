package balance

import (
	"context"
	"database/sql"
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

func TestRepository_WithdrawLocksBeforeConditionalInsert(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	ctx := context.WithValue(context.Background(), repositoryContextKey{}, "withdraw")
	var queries []string
	var variables [][]any
	var contexts []context.Context
	require.NoError(t, gormDB.Callback().Raw().After("testkit:dry-run-exec-result").Register(
		"test:observe-withdraw",
		func(tx *gorm.DB) {
			queries = append(queries, tx.Statement.SQL.String())
			variables = append(variables, slices.Clone(tx.Statement.Vars))
			contexts = append(contexts, tx.Statement.Context)
		},
	))
	testkit.SetExecResult(gormDB, 1, nil)

	outcome, err := New(session).Withdraw(ctx, 42, "2377225624", money.Points(75100))

	require.NoError(t, err)
	assert.Equal(t, domain.Withdrawn, outcome)
	require.Len(t, queries, 2)
	assert.Contains(t, queries[0], "SELECT pg_advisory_xact_lock($1)")
	assert.Equal(t, []any{int64(42)}, variables[0])
	insert := strings.Join(strings.Fields(queries[1]), " ")
	assert.Contains(t, insert, `INSERT INTO withdrawals (user_id, order_number, sum) SELECT $1, $2, $3`)
	assert.Contains(t, insert, `WHERE ( SELECT accrued - withdrawn`)
	assert.Contains(t, insert, `FROM ( SELECT`)
	assert.Contains(t, insert, `(SELECT COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0) FROM orders WHERE user_id = $4) AS accrued`)
	assert.Contains(t, insert, `(SELECT COALESCE(SUM(sum), 0) FROM withdrawals WHERE user_id = $5) AS withdrawn`)
	assert.Contains(t, insert, `AS balance_totals`)
	assert.Contains(t, insert, `) >= $6`)
	assert.Contains(t, insert, `ON CONFLICT (order_number) DO NOTHING`)
	assert.Equal(t, []any{
		int64(42),
		"2377225624",
		money.Points(75100),
		int64(42),
		int64(42),
		money.Points(75100),
	}, variables[1])
	assert.Equal(t, []context.Context{ctx, ctx}, contexts)
	assert.Equal(t, testkit.TransactionState{
		Begun:     1,
		Committed: 1,
		Isolation: sql.LevelReadCommitted,
	}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_WithdrawClassifiesUnchangedInsert(t *testing.T) {
	tests := []struct {
		name     string
		rows     []withdrawalOwnerRow
		queryErr error
		want     domain.WithdrawOutcome
	}{
		{
			name:     "not enough funds",
			queryErr: gorm.ErrRecordNotFound,
			want:     domain.NotEnoughFunds,
		},
		{
			name: "same user already withdrew",
			rows: []withdrawalOwnerRow{{UserID: 42}},
			want: domain.AlreadyWithdrawn,
		},
		{
			name: "number belongs to another user",
			rows: []withdrawalOwnerRow{{UserID: 7}},
			want: domain.TakenByAnother,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, gormDB := testkit.NewDryRunSession(t)
			testkit.SetExecResult(gormDB, 0, nil)
			testkit.SetQueryResult(gormDB, test.rows, test.queryErr)
			ctx := context.WithValue(context.Background(), repositoryContextKey{}, test.name)
			var query string
			var variables []any
			var operationContext context.Context
			require.NoError(t, gormDB.Callback().Query().After("testkit:dry-run-query-result").Register(
				"test:observe-withdraw-owner",
				func(tx *gorm.DB) {
					query = tx.Statement.SQL.String()
					variables = slices.Clone(tx.Statement.Vars)
					operationContext = tx.Statement.Context
				},
			))

			outcome, err := New(session).Withdraw(ctx, 42, "2377225624", money.Points(75100))

			require.NoError(t, err)
			assert.Equal(t, test.want, outcome)
			assert.Contains(t, query, `SELECT "user_id" FROM "withdrawals" WHERE order_number = $1`)
			assert.Equal(t, []any{"2377225624", 1}, variables)
			assert.Same(t, ctx, operationContext)
			assert.Equal(t, testkit.TransactionState{
				Begun:     1,
				Committed: 1,
				Isolation: sql.LevelReadCommitted,
			}, testkit.TransactionStateOf(t, gormDB))
		})
	}
}

func TestRepository_WithdrawRollsBackOnAdvisoryLockError(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetExecResult(gormDB, 1, nil)
	var rawQueries []string
	require.NoError(t, gormDB.Callback().Raw().After("testkit:dry-run-exec-result").Register(
		"test:fail-withdraw-lock",
		func(tx *gorm.DB) {
			query := tx.Statement.SQL.String()
			rawQueries = append(rawQueries, query)
			if strings.Contains(query, "pg_advisory_xact_lock") {
				tx.AddError(storageErr)
			}
		},
	))

	_, err := New(session).Withdraw(context.Background(), 42, "2377225624", money.Points(75100))

	require.ErrorIs(t, err, storageErr)
	require.Len(t, rawQueries, 1)
	assert.Contains(t, rawQueries[0], "SELECT pg_advisory_xact_lock")
	assert.NotContains(t, rawQueries[0], "INSERT INTO withdrawals")
	assert.Equal(t, testkit.TransactionState{
		Begun:      1,
		RolledBack: 1,
		Isolation:  sql.LevelReadCommitted,
	}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_WithdrawRollsBackOnConditionalInsertError(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetExecResult(gormDB, 1, nil)
	var rawQueries []string
	require.NoError(t, gormDB.Callback().Raw().After("testkit:dry-run-exec-result").Register(
		"test:fail-withdraw-insert",
		func(tx *gorm.DB) {
			rawQueries = append(rawQueries, tx.Statement.SQL.String())
			if len(rawQueries) == 2 {
				tx.AddError(storageErr)
			}
		},
	))

	_, err := New(session).Withdraw(context.Background(), 42, "2377225624", money.Points(75100))

	require.ErrorIs(t, err, storageErr)
	require.Len(t, rawQueries, 2, "ошибка внедряется после успешно выполненного замка")
	assert.Contains(t, rawQueries[0], "SELECT pg_advisory_xact_lock")
	assert.Contains(t, rawQueries[1], "INSERT INTO withdrawals")
	assert.Equal(t, testkit.TransactionState{
		Begun:      1,
		RolledBack: 1,
		Isolation:  sql.LevelReadCommitted,
	}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_WithdrawRollsBackOnOwnerLookupError(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetExecResult(gormDB, 0, nil)
	testkit.SetQueryResult(gormDB, []withdrawalOwnerRow(nil), storageErr)

	_, err := New(session).Withdraw(context.Background(), 42, "2377225624", money.Points(75100))

	require.ErrorIs(t, err, storageErr)
	assert.Equal(t, testkit.TransactionState{
		Begun:      1,
		RolledBack: 1,
		Isolation:  sql.LevelReadCommitted,
	}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_WithdrawWithoutDatabaseIsControlled(t *testing.T) {
	_, err := New(database.Session{}).Withdraw(context.Background(), 42, "2377225624", money.Points(75100))

	require.ErrorIs(t, err, database.ErrUnavailable)
}
