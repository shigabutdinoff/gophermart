package order

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

func newRepositoryWithPusher(t *testing.T, session database.Session, pusher Pusher) *Repository {
	t.Helper()

	repository := New(session)
	require.NoError(t, repository.AttachPusher(pusher))

	return repository
}

// applyResultCallback именует подмену записи, каждый тест берёт свою фикстуру.
const applyResultCallback = "test:apply-result"

// applyResultUpdate подставляет ответ RETURNING вместо настоящей записи.
func applyResultUpdate(
	t *testing.T,
	gormDB *gorm.DB,
	rows int64,
	returned domain.Status,
	observe func(*gorm.DB),
) {
	t.Helper()

	require.NoError(t, gormDB.Callback().Update().After("gorm:update").Register(
		applyResultCallback,
		func(tx *gorm.DB) {
			if observe != nil {
				observe(tx)
			}
			tx.RowsAffected = rows
			if row, ok := tx.Statement.Model.(*orderRow); ok {
				row.Status = returned
			}
		},
	))
}
