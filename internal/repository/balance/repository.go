package balance

import (
	"context"
	"fmt"

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

const withdrawSQL = `
INSERT INTO withdrawals (user_id, order_number, sum)
SELECT ?, ?, ?
WHERE (
    SELECT COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0)
      FROM orders WHERE user_id = ?
) - (
    SELECT COALESCE(SUM(sum), 0) FROM withdrawals WHERE user_id = ?
) >= ?`

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

// Withdraw записывает списание, если вычисленного остатка достаточно.
func (r *Repository) Withdraw(
	ctx context.Context,
	userID int64,
	number string,
	sum money.Points,
) (domain.WithdrawOutcome, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return 0, err
	}

	insert := db.Exec(withdrawSQL, userID, number, sum, userID, userID, sum)
	if insert.Error != nil {
		return 0, fmt.Errorf("insert withdrawal: %w", insert.Error)
	}
	if insert.RowsAffected == 0 {
		return domain.NotEnoughFunds, nil
	}

	return domain.Withdrawn, nil
}
