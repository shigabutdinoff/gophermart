package order

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/money"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/testkit"
)

func TestRepository_ListByUserReturnsStoredOrders(t *testing.T) {
	gormDB := testkit.NewDryRunDB(t)
	uploadedAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	var query string
	var variables []any
	require.NoError(t, gormDB.Callback().Query().After("testkit:dry-run-query-result").Register(
		"test:observe-list-by-user",
		func(tx *gorm.DB) {
			query = tx.Statement.SQL.String()
			variables = slices.Clone(tx.Statement.Vars)
		},
	))
	accrued := money.Points(50050)
	testkit.SetQueryResult(t, gormDB, []orderRow{{
		Number:     "12345678903",
		UserID:     42,
		Status:     domain.StatusProcessed,
		UploadedAt: uploadedAt,
		Accrual:    &accrued,
	}}, nil)

	list, err := New(gormDB).ListByUser(context.Background(), 42)

	require.NoError(t, err)
	assert.Equal(t, []domain.Order{{
		Number:     "12345678903",
		UserID:     42,
		Status:     domain.StatusProcessed,
		UploadedAt: uploadedAt,
		Accrual:    &accrued,
	}}, list)
	assert.Contains(t, query, `WHERE user_id = $1`)
	assert.Contains(t, query, `ORDER BY uploaded_at DESC, id DESC`)
	assert.Equal(t, []any{int64(42)}, variables)
}
