package order

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/money"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_ApplyResultWritesIntermediateOrderInOneStatement(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	var query string
	var variables []any
	applyResultUpdate(t, gormDB, "test:observe-apply-result", 1, domain.StatusProcessing, func(tx *gorm.DB) {
		query = tx.Statement.SQL.String()
		variables = slices.Clone(tx.Statement.Vars)
	})
	accrued := money.Points(50050)

	finished, err := New(gormDB).ApplyResult(
		context.Background(),
		"12345678903",
		domain.StatusProcessing,
		&accrued,
	)

	require.NoError(t, err)
	assert.False(t, finished, "незавершённый заказ опрашивают дальше")
	assert.Contains(t, query, `UPDATE "orders" SET`)
	assert.Contains(t, query, `"accrual"=CASE WHEN status IN ($1,$2) THEN accrual ELSE $3 END`)
	assert.Contains(t, query, `"status"=CASE WHEN status IN ($4,$5) THEN status ELSE $6 END`)
	assert.Contains(t, query, `WHERE number = $7`)
	assert.Contains(t, query, `RETURNING "status"`)
	assert.NotContains(t, query, `SELECT`, "исход пишется одним запросом")
	assert.Equal(
		t,
		[]any{
			domain.StatusProcessed,
			domain.StatusInvalid,
			&accrued,
			domain.StatusProcessed,
			domain.StatusInvalid,
			domain.StatusProcessing,
			"12345678903",
		},
		variables,
	)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_ApplyResultReportsFinishedOrder(t *testing.T) {
	for _, status := range domain.FinalStatuses() {
		t.Run(string(status), func(t *testing.T) {
			gormDB := testkit.NewDryRunDB(t)
			applyResultUpdate(t, gormDB, "test:observe-final-apply-result", 1, status, nil)

			finished, err := New(gormDB).ApplyResult(
				context.Background(),
				"12345678903",
				status,
				nil,
			)

			require.NoError(t, err)
			assert.True(t, finished)
		})
	}
}

// Запись завершённого заказа выдаёт себя ответом: строка вернула прежний
// окончательный статус, а не тот, который в неё пытались записать.
func TestRepository_ApplyResultKeepsOrderClosedByAnotherAttempt(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	applyResultUpdate(t, gormDB, "test:apply-result-hits-closed-order", 1, domain.StatusInvalid, nil)
	accrued := money.Points(50050)

	finished, err := New(gormDB).ApplyResult(
		context.Background(),
		"12345678903",
		domain.StatusProcessing,
		&accrued,
	)

	require.NoError(t, err)
	assert.True(t, finished, "закрытый чужой попыткой заказ больше не опрашивают")
}

func TestRepository_ApplyResultReportsVanishedOrder(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	applyResultUpdate(t, gormDB, "test:apply-result-misses-order", 0, "", nil)

	finished, err := New(gormDB).ApplyResult(
		context.Background(),
		"12345678903",
		domain.StatusProcessing,
		nil,
	)

	require.ErrorIs(t, err, domain.ErrNotFound, "пропавшую строку не вернёт ни одна попытка")
	assert.ErrorContains(t, err, "apply order result")
	assert.False(t, finished)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_ApplyResultReportsWriteError(t *testing.T) {
	storageErr := errors.New("storage")
	gormDB := testkit.NewDryRunDB(t)
	require.NoError(t, gormDB.Callback().Update().After("gorm:update").Register(
		"test:failing-apply-result",
		func(tx *gorm.DB) { tx.AddError(storageErr) },
	))

	finished, err := New(gormDB).ApplyResult(
		context.Background(),
		"12345678903",
		domain.StatusProcessing,
		nil,
	)

	require.ErrorIs(t, err, storageErr)
	assert.False(t, finished)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_ApplyResultWithoutDatabaseIsControlled(t *testing.T) {
	finished, err := New(nil).ApplyResult(
		context.Background(),
		"12345678903",
		domain.StatusProcessed,
		nil,
	)

	require.ErrorIs(t, err, database.ErrUnavailable)
	assert.False(t, finished)
}
