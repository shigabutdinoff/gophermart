package testkit

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSetExecResultControlsDryRunExec(t *testing.T) {
	tests := []struct {
		name         string
		rowsAffected int64
		execErr      error
	}{
		{name: "rows affected", rowsAffected: 7},
		{name: "error", execErr: errors.New("write failed")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gormDB := NewDryRunDB(t)
			SetExecResult(gormDB, test.rowsAffected, test.execErr)

			result := gormDB.Exec("UPDATE things SET value = ?", 42)

			assert.Equal(t, test.rowsAffected, result.RowsAffected)
			require.ErrorIs(t, result.Error, test.execErr)
		})
	}
}

func TestDryRunDBWithoutSQLTransactionHidesSQLTx(t *testing.T) {
	gormDB := NewDryRunDBWithoutSQLTransaction(t)

	require.NoError(t, gormDB.Transaction(func(tx *gorm.DB) error {
		_, isSQLTx := tx.Statement.ConnPool.(*sql.Tx)
		assert.False(t, isSQLTx, "фикстура обязана скрыть транзакцию database/sql")

		return nil
	}))

	assert.Equal(t, TransactionState{Begun: 1, Committed: 1}, TransactionStateOf(t, gormDB))
}

func TestDryRunDBWithoutSQLTransactionCountsRollback(t *testing.T) {
	gormDB := NewDryRunDBWithoutSQLTransaction(t)
	transactionErr := errors.New("transaction failed")

	err := gormDB.Transaction(func(*gorm.DB) error { return transactionErr })

	require.ErrorIs(t, err, transactionErr)
	assert.Equal(t, TransactionState{Begun: 1, RolledBack: 1}, TransactionStateOf(t, gormDB))
}
