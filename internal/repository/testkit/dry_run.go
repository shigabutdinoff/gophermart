package testkit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const (
	createResultKey     = "testkit:dry-run-create-result"
	execResultKey       = "testkit:dry-run-exec-result"
	queryResultKey      = "testkit:dry-run-query-result"
	transactionStateKey = "testkit:dry-run-transaction-state"
)

// TransactionState хранит результат жизненного цикла транзакции в DryRun-тесте.
type TransactionState struct {
	Begun      int
	Committed  int
	RolledBack int
	Isolation  sql.IsolationLevel
}

// transactionRecorder синхронизирует счётчики: откат отменённой транзакции
// database/sql выполняет фоновой горутиной, а читает их тест.
type transactionRecorder struct {
	mu    sync.Mutex
	state TransactionState
}

func (r *transactionRecorder) begin(isolation sql.IsolationLevel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Begun++
	r.state.Isolation = isolation
}

func (r *transactionRecorder) commit() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Committed++
}

func (r *transactionRecorder) rollback() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.RolledBack++
}

func (r *transactionRecorder) snapshot() TransactionState {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.state
}

// dryRunDriver отдаёт настоящие транзакции database/sql, не открывая БД:
// очередь заданий ждёт *sql.Tx, и подделка тут скрыла бы ошибку проводки.
type dryRunDriver struct{}

func (dryRunDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connections come from Connect")
}

type dryRunConnector struct {
	recorder *transactionRecorder
}

func (c *dryRunConnector) Connect(context.Context) (driver.Conn, error) {
	return &dryRunConnection{recorder: c.recorder}, nil
}

func (*dryRunConnector) Driver() driver.Driver { return dryRunDriver{} }

type dryRunConnection struct {
	recorder *transactionRecorder
}

func (*dryRunConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("query is not supported")
}

func (*dryRunConnection) Close() error { return nil }

func (c *dryRunConnection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *dryRunConnection) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.recorder.begin(sql.IsolationLevel(opts.Isolation))

	return &dryRunTransaction{recorder: c.recorder}, nil
}

type dryRunTransaction struct {
	recorder *transactionRecorder
}

func (tx *dryRunTransaction) Commit() error {
	tx.recorder.commit()

	return nil
}

func (tx *dryRunTransaction) Rollback() error {
	tx.recorder.rollback()

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

// NewDryRunSession отдаёт репозиторию сессию, а тесту ручку фикстуры.
func NewDryRunSession(t testing.TB) (database.Session, *gorm.DB) {
	t.Helper()

	gormDB := NewDryRunDB(t)

	return database.NewSession(gormDB), gormDB
}

func NewDryRunDB(t testing.TB) *gorm.DB {
	t.Helper()

	gormDB, _ := newDryRunDB(t)

	return gormDB
}

func newDryRunDB(t testing.TB) (*gorm.DB, *transactionRecorder) {
	t.Helper()

	recorder := &transactionRecorder{}
	sqlDB := sql.OpenDB(&dryRunConnector{recorder: recorder})
	gormDB := openGORM(t, sqlDB).Session(&gorm.Session{
		DryRun:                 true,
		SkipDefaultTransaction: true,
	})
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close GORM DryRun database: %v", err)
		}
	})
	gormDB.Statement.Settings.Store(transactionStateKey, recorder)

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

	return gormDB, recorder
}

// NewDryRunDBWithoutSQLTransaction даёт фикстуру, чья транзакция не *sql.Tx:
// так проверяется отказ поставить задание мимо транзакции заказа.
func NewDryRunDBWithoutSQLTransaction(t testing.TB) *gorm.DB {
	t.Helper()

	gormDB, recorder := newDryRunDB(t)
	gormDB.Statement.ConnPool = &foreignPool{
		ConnPool: gormDB.Statement.ConnPool,
		recorder: recorder,
	}

	return gormDB
}

// foreignPool начинает транзакцию так, как это делает драйвер без
// database/sql: gorm получает свой ConnPool, а не *sql.Tx.
type foreignPool struct {
	gorm.ConnPool
	recorder *transactionRecorder
}

func (p *foreignPool) BeginTx(_ context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	isolation := sql.LevelDefault
	if opts != nil {
		isolation = opts.Isolation
	}
	p.recorder.begin(isolation)

	return &foreignTransaction{ConnPool: p.ConnPool, recorder: p.recorder}, nil
}

type foreignTransaction struct {
	gorm.ConnPool
	recorder *transactionRecorder
}

func (tx *foreignTransaction) Commit() error {
	tx.recorder.commit()

	return nil
}

func (tx *foreignTransaction) Rollback() error {
	tx.recorder.rollback()

	return nil
}

// TransactionStateOf возвращает состояние транзакций DryRun-фикстуры.
func TransactionStateOf(t testing.TB, db *gorm.DB) TransactionState {
	t.Helper()

	stored, ok := db.Statement.Settings.Load(transactionStateKey)
	if !ok {
		t.Fatal("DryRun transaction state is not configured")
	}

	return stored.(*transactionRecorder).snapshot()
}

// SetCreateResult задаёт результат вставки для DryRun-фикстуры.
func SetCreateResult(db *gorm.DB, rowsAffected int64, err error) {
	db.Statement.Settings.Store(createResultKey, createResult{rowsAffected: rowsAffected, err: err})
}

// SetExecResult задаёт результат сырой записи для DryRun-фикстуры.
func SetExecResult(db *gorm.DB, rowsAffected int64, err error) {
	db.Statement.Settings.Store(execResultKey, execResult{rowsAffected: rowsAffected, err: err})
}

// SetQueryResult задаёт строки и ошибку чтения для DryRun-фикстуры.
func SetQueryResult[T any](db *gorm.DB, rows []T, err error) {
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
