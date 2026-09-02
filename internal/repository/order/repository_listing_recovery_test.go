package order

import (
	"context"
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
