package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/money"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_ListByUserReturnsStoredOrders(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	uploadedAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	observed := testkit.ObserveStatements(t, gormDB)
	accrued := money.Points(50050)
	testkit.SetQueryResult(gormDB, []orderRow{{
		Number:     "12345678903",
		Status:     domain.StatusProcessed,
		UploadedAt: uploadedAt,
		Accrual:    &accrued,
	}}, nil)

	list, err := New(session).ListByUser(context.Background(), 42)

	require.NoError(t, err)
	assert.Equal(t, []domain.Order{{
		Number:     "12345678903",
		Status:     domain.StatusProcessed,
		UploadedAt: uploadedAt,
		Accrual:    &accrued,
	}}, list)
	statement := observed()
	assert.Contains(t, statement.SQL, `WHERE user_id = $1`)
	assert.Contains(t, statement.SQL, `ORDER BY uploaded_at DESC, id DESC`)
	assert.Equal(t, []any{int64(42)}, statement.Variables)
}

func TestRepository_ListUnfinishedReturnsOnlyUnfinishedNumbers(t *testing.T) {
	session, gormDB := testkit.NewDryRunSession(t)
	observed := testkit.ObserveStatements(t, gormDB)
	testkit.SetQueryResult(gormDB, []string{"12345678903", "2377225624"}, nil)

	got, err := New(session).ListUnfinished(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"12345678903", "2377225624"}, got)
	statement := observed()
	assert.Contains(t, statement.SQL, `SELECT number FROM "orders"`)
	assert.Contains(t, statement.SQL, `WHERE status NOT IN ($1,$2)`)
	assert.Equal(t, []any{domain.StatusProcessed, domain.StatusInvalid}, statement.Variables)
	assert.Equal(t, testkit.TransactionState{}, testkit.TransactionStateOf(t, gormDB))
}

func TestRepository_ListUnfinishedKeepsDatabaseError(t *testing.T) {
	storageErr := errors.New("storage")
	session, gormDB := testkit.NewDryRunSession(t)
	testkit.SetQueryResult(gormDB, []string(nil), storageErr)

	_, err := New(session).ListUnfinished(context.Background())

	require.ErrorIs(t, err, storageErr)
}
