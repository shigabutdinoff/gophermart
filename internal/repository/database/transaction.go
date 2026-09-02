package database

import (
	"database/sql"
	"errors"

	"gorm.io/gorm"
)

// ErrOutsideTransaction ловит подключение, за которым нет транзакции
// database/sql.
var ErrOutsideTransaction = errors.New("connection is not a sql transaction")

// Tx держит одну транзакцию в двух видах. Репозиторий запрашивает через
// gorm, а клиентам очереди нужна та же *sql.Tx.
type Tx struct {
	db *gorm.DB
}

// DB отдаёт транзакцию в терминах gorm.
func (t Tx) DB() *gorm.DB {
	return t.db
}

// SQL достаёт транзакцию, которую gorm держит в ConnPool. Внутренности
// gorm дальше этого пакета не идут.
func (t Tx) SQL() (*sql.Tx, error) {
	sqlTx, ok := t.db.Statement.ConnPool.(*sql.Tx)
	if !ok {
		return nil, ErrOutsideTransaction
	}

	return sqlTx, nil
}

// Transact выполняет работу в одной транзакции подключения.
func Transact(db *gorm.DB, work func(Tx) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return work(Tx{db: tx})
	})
}
