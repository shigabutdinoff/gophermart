package balance

import (
	"context"

	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const ordersTable = "orders"

const balanceExpression = `
COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0)
    - (SELECT COALESCE(SUM(sum), 0) FROM withdrawals WHERE user_id = ?) AS current,
(SELECT COALESCE(SUM(sum), 0) FROM withdrawals WHERE user_id = ?) AS withdrawn`

type balanceRow struct {
	Current   money.Points `gorm:"column:current"`
	Withdrawn money.Points `gorm:"column:withdrawn"`
}

func (r balanceRow) balance() domain.Balance {
	return domain.Balance{Current: r.Current, Withdrawn: r.Withdrawn}
}

// Repository считает счёт по сохранённым заказам и списаниям.
type Repository struct {
	session database.Session
}

// New принимает nil вместо БД, тогда репозиторий отвечает отказом.
func New(db *gorm.DB) *Repository {
	return &Repository{session: database.NewSession(db)}
}

// Balance считает начисленные и списанные баллы одним запросом.
func (r *Repository) Balance(ctx context.Context, userID int64) (domain.Balance, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return domain.Balance{}, err
	}

	var row balanceRow
	if err := db.Table(ordersTable).
		Select(balanceExpression, userID, userID).
		Where("user_id = ?", userID).
		Take(&row).Error; err != nil {
		return domain.Balance{}, err
	}

	return row.balance(), nil
}
