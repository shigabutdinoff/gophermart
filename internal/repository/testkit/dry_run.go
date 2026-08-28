package testkit

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const dryRunDSN = "postgres://gophermart:password@127.0.0.1:1/gophermart?sslmode=disable"

const (
	createResultKey = "testkit:dry-run-create-result"
	execResultKey   = "testkit:dry-run-exec-result"
	queryResultKey  = "testkit:dry-run-query-result"
)

// TransactionState хранит результат жизненного цикла транзакции в DryRun-тесте.
type TransactionState struct {
	Begun      int
	Committed  int
	RolledBack int
	Isolation  sql.IsolationLevel
}

type dryRunConnPool struct {
	gorm.ConnPool
	state *TransactionState
}

func (p *dryRunConnPool) BeginTx(
	_ context.Context,
	options *sql.TxOptions,
) (gorm.ConnPool, error) {
	p.state.Begun++
	if options != nil {
		p.state.Isolation = options.Isolation
	}

	return &dryRunTransaction{ConnPool: p.ConnPool, state: p.state}, nil
}

type dryRunTransaction struct {
	gorm.ConnPool
	state *TransactionState
}

func (tx *dryRunTransaction) Commit() error {
	tx.state.Committed++

	return nil
}

func (tx *dryRunTransaction) Rollback() error {
	tx.state.RolledBack++

	return nil
}

type createResult struct {
	rowsAffected int64
	err          error
}

type execResult struct {
	rowsAffected int64
	err          error
}

type queryResult struct {
	apply func(any)
	err   error
}

func NewDryRunDB(t testing.TB) *gorm.DB {
	t.Helper()

	gormDB, err := database.Connection(dryRunDSN)
	if err != nil {
		t.Fatalf("open GORM DryRun database: %v", err)
	}

	gormDB = gormDB.Session(&gorm.Session{
		DryRun:                 true,
		SkipDefaultTransaction: true,
	})
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("get underlying DryRun database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close GORM DryRun database: %v", err)
		}
	})

	state := &TransactionState{}
	pool := &dryRunConnPool{ConnPool: gormDB.Statement.ConnPool, state: state}
	gormDB.Config.ConnPool = pool
	gormDB.Statement.ConnPool = pool
	requireCallback(t, gormDB.Callback().Create().After("gorm:create").Register(
		createResultKey,
		func(tx *gorm.DB) {
			result := createResult{rowsAffected: 1}
			if configured, ok := tx.Get(createResultKey); ok {
				result = configured.(createResult)
			}
			if result.err != nil {
				tx.AddError(result.err)

				return
			}
			tx.RowsAffected = result.rowsAffected
		},
	))
	requireCallback(t, gormDB.Callback().Raw().After("gorm:raw").Register(
		execResultKey,
		func(tx *gorm.DB) {
			configured, ok := tx.Get(execResultKey)
			if !ok {
				return
			}
			result := configured.(execResult)
			tx.RowsAffected = result.rowsAffected
			if result.err != nil {
				tx.AddError(result.err)
			}
		},
	))
	requireCallback(t, gormDB.Callback().Query().After("gorm:query").Register(
		queryResultKey,
		func(tx *gorm.DB) {
			configured, ok := tx.Get(queryResultKey)
			if !ok {
				return
			}
			result := configured.(queryResult)
			if result.apply != nil {
				result.apply(tx.Statement.Dest)
			}
			if result.err != nil {
				tx.AddError(result.err)
			}
		},
	))

	return gormDB
}

// TransactionStateOf возвращает состояние транзакций, начатых через DryRun-фикстуру.
func TransactionStateOf(t testing.TB, db *gorm.DB) TransactionState {
	t.Helper()

	pool, ok := db.Statement.ConnPool.(*dryRunConnPool)
	if !ok {
		t.Fatal("DryRun transaction pool is not configured")
	}

	return *pool.state
}

// SetCreateResult задаёт результат вставки для DryRun-фикстуры.
func SetCreateResult(t testing.TB, db *gorm.DB, rowsAffected int64, err error) {
	t.Helper()
	db.Statement.Settings.Store(createResultKey, createResult{rowsAffected: rowsAffected, err: err})
}

// SetExecResult задаёт результат сырой записи для DryRun-фикстуры.
func SetExecResult(t testing.TB, db *gorm.DB, rowsAffected int64, err error) {
	t.Helper()
	db.Statement.Settings.Store(execResultKey, execResult{rowsAffected: rowsAffected, err: err})
}

// SetQueryResult задаёт строки и ошибку чтения для DryRun-фикстуры.
func SetQueryResult[T any](t testing.TB, db *gorm.DB, rows []T, err error) {
	t.Helper()
	resultRows := slices.Clone(rows)
	db.Statement.Settings.Store(queryResultKey, queryResult{
		apply: func(destination any) {
			switch destination := destination.(type) {
			case *T:
				if len(resultRows) > 0 {
					*destination = resultRows[0]
				}
			case *[]T:
				*destination = slices.Clone(resultRows)
			}
		},
		err: err,
	})
}

func requireCallback(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("register DryRun callback: %v", err)
	}
}
