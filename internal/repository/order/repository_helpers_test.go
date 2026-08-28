package order

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/order"
)

type pusherState struct {
	transactions []*gorm.DB
	numbers      []string
	err          error
}

func (s *pusherState) mock(t *testing.T, expectedCalls int) *MockPusher {
	t.Helper()

	pusher := NewMockPusher(t)
	if expectedCalls == 0 {
		return pusher
	}
	pusher.EXPECT().Push(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, tx *gorm.DB, number string) {
			s.transactions = append(s.transactions, tx)
			s.numbers = append(s.numbers, number)
		}).
		Return(s.err).
		Times(expectedCalls)

	return pusher
}

func newRepositoryWithPusher(t *testing.T, db *gorm.DB, pusher Pusher) *Repository {
	t.Helper()

	repository := New(db)
	require.NoError(t, repository.AttachPusher(pusher))

	return repository
}

// applyResultUpdate подставляет ответ RETURNING вместо настоящей записи.
func applyResultUpdate(
	t *testing.T,
	gormDB *gorm.DB,
	name string,
	rows int64,
	returned domain.Status,
	observe func(*gorm.DB),
) {
	t.Helper()

	require.NoError(t, gormDB.Callback().Update().After("gorm:update").Register(
		name,
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

type repositoryContextKey struct{}
