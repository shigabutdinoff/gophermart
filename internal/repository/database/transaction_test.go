package database_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestTransact_GivesBothSidesOfOneTransaction(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)

	require.NoError(t, database.Transact(gormDB, func(tx database.Tx) error {
		assert.NotNil(t, tx.DB())
		sqlTx, err := tx.SQL()
		require.NoError(t, err)
		assert.NotNil(t, sqlTx)

		return nil
	}))

	assert.Equal(t, testkit.TransactionState{Begun: 1, Committed: 1}, testkit.TransactionStateOf(t, gormDB))
}

func TestTransact_RollsBackFailedWork(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	workErr := errors.New("work failed")

	err := database.Transact(gormDB, func(database.Tx) error { return workErr })

	require.ErrorIs(t, err, workErr)
	assert.Equal(t, testkit.TransactionState{Begun: 1, RolledBack: 1}, testkit.TransactionStateOf(t, gormDB))
}

// Клиенты очереди ждут ту же *sql.Tx, поэтому чужое подключение
// репозиторию отдавать нельзя.
func TestTx_SQLRefusesConnectionOutsideTransaction(t *testing.T) {
	gormDB := testkit.NewDryRunDBWithoutSQLTransaction(t)

	err := database.Transact(gormDB, func(tx database.Tx) error {
		sqlTx, err := tx.SQL()
		assert.Nil(t, sqlTx)

		return err
	})

	require.ErrorIs(t, err, database.ErrOutsideTransaction)
}
