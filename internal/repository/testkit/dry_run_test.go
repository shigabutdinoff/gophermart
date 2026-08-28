package testkit

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			SetExecResult(t, gormDB, test.rowsAffected, test.execErr)

			result := gormDB.Exec("UPDATE things SET value = ?", 42)

			assert.Equal(t, test.rowsAffected, result.RowsAffected)
			require.ErrorIs(t, result.Error, test.execErr)
		})
	}
}
