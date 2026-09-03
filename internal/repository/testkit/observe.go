package testkit

import (
	"context"
	"slices"
	"testing"

	"gorm.io/gorm"
)

const observeStatementsKey = "testkit:observe-statements"

// Statement — снимок того, что gorm собрал для отправки в пул.
type Statement struct {
	Context   context.Context
	SQL       string
	Variables []any
	Dest      any
	Calls     int
}

// ObserveStatements запоминает последний собранный запрос любого рода.
// Наблюдатели встают после подстановки результата, поэтому видят и SQL,
// и заполненный Dest.
func ObserveStatements(t testing.TB, db *gorm.DB) func() Statement {
	t.Helper()

	var observed Statement
	capture := func(tx *gorm.DB) {
		observed.Calls++
		observed.Context = tx.Statement.Context
		observed.SQL = tx.Statement.SQL.String()
		observed.Variables = slices.Clone(tx.Statement.Vars)
		observed.Dest = tx.Statement.Dest
	}
	requireCallback(t, db.Callback().Create().After(createResultKey).Register(
		observeStatementsKey,
		capture,
	))
	requireCallback(t, db.Callback().Query().After(queryResultKey).Register(
		observeStatementsKey,
		capture,
	))
	requireCallback(t, db.Callback().Raw().After(execResultKey).Register(
		observeStatementsKey,
		capture,
	))

	return func() Statement { return observed }
}
