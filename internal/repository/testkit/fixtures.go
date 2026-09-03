package testkit

import (
	"database/sql"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

// openGORM собирает gorm поверх готового пула на боевых настройках.
// Пул остаётся за вызывающим, закрывать его здесь нельзя.
func openGORM(t testing.TB, sqlDB *sql.DB) *gorm.DB {
	t.Helper()

	gormDB, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB, PreferSimpleProtocol: true}),
		database.Config(),
	)
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}

	return gormDB
}

// OpenDryRunGORM собирает запросы, не отправляя их в пул.
// DryRun задаётся сессией, потому что через Config теряются Settings.
func OpenDryRunGORM(t testing.TB, sqlDB *sql.DB) *gorm.DB {
	t.Helper()

	return openGORM(t, sqlDB).Session(&gorm.Session{DryRun: true})
}

// FailStatements обрывает вставку и чтение заданной ошибкой.
func FailStatements(t testing.TB, db *gorm.DB, err error) {
	t.Helper()

	fail := func(tx *gorm.DB) { tx.AddError(err) }
	requireCallback(t, db.Callback().Create().Before("gorm:create").Register(
		"testkit:fail-create",
		fail,
	))
	requireCallback(t, db.Callback().Query().Before("gorm:query").Register(
		"testkit:fail-query",
		fail,
	))
}

// ObserveCreatePool запоминает пул, которым репозиторий выполняет вставку.
func ObserveCreatePool(t testing.TB, db *gorm.DB) func() *sql.DB {
	t.Helper()

	var pool *sql.DB
	requireCallback(t, db.Callback().Create().Before("gorm:create").Register(
		"testkit:observe-create-pool",
		func(tx *gorm.DB) { pool, _ = tx.DB() },
	))

	return func() *sql.DB { return pool }
}
