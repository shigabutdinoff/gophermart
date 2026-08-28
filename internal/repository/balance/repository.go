package balance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const balanceAggregatesSQL = `
SELECT
    (SELECT COALESCE(SUM(accrual) FILTER (WHERE status = 'PROCESSED'), 0)
       FROM orders WHERE user_id = ?) AS accrued,
    (SELECT COALESCE(SUM(sum), 0)
       FROM withdrawals WHERE user_id = ?) AS withdrawn`

const advisoryLockSQL = `SELECT pg_advisory_xact_lock(?)`

const withdrawSQL = `
INSERT INTO withdrawals (user_id, order_number, sum)
SELECT ?, ?, ?
WHERE (
    SELECT accrued - withdrawn
      FROM (` + balanceAggregatesSQL + `) AS balance_totals
) >= ?
ON CONFLICT (order_number) DO NOTHING`

type balanceRow struct {
	Current   money.Points `gorm:"column:current"`
	Withdrawn money.Points `gorm:"column:withdrawn"`
}

type withdrawalOwnerRow struct {
	UserID int64 `gorm:"column:user_id"`
}

type withdrawalRow struct {
	Order       string       `gorm:"column:order_number"`
	Sum         money.Points `gorm:"column:sum"`
	ProcessedAt time.Time    `gorm:"column:processed_at"`
}

func (r balanceRow) balance() domain.Balance {
	return domain.Balance{Current: r.Current, Withdrawn: r.Withdrawn}
}

func (r withdrawalRow) withdrawal() domain.Withdrawal {
	return domain.Withdrawal{Order: r.Order, Sum: r.Sum, ProcessedAt: r.ProcessedAt}
}

// Repository считает счёт по сохранённым заказам и списаниям.
type Repository struct {
	session database.Session
}

// New принимает нулевую сессию вместо БД, тогда репозиторий отвечает отказом.
func New(session database.Session) *Repository {
	return &Repository{session: session}
}

// Balance считает начисленные и списанные баллы одним запросом.
func (r *Repository) Balance(ctx context.Context, userID int64) (domain.Balance, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return domain.Balance{}, err
	}

	var row balanceRow
	if err := db.Table("(?) AS balance_totals", db.Raw(balanceAggregatesSQL, userID, userID)).
		Select("accrued - withdrawn AS current, withdrawn").
		Take(&row).Error; err != nil {
		return domain.Balance{}, err
	}

	return row.balance(), nil
}

// ListWithdrawals отдаёт историю списаний пользователя от новых к старым.
func (r *Repository) ListWithdrawals(ctx context.Context, userID int64) ([]domain.Withdrawal, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return nil, err
	}

	var rows []withdrawalRow
	if err := db.Table("withdrawals").
		Select("order_number, sum, processed_at").
		Where("user_id = ?", userID).
		Order("processed_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	withdrawals := make([]domain.Withdrawal, len(rows))
	for i, row := range rows {
		withdrawals[i] = row.withdrawal()
	}

	return withdrawals, nil
}

// Withdraw сериализует списания пользователя и атомарно проверяет остаток.
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

	var outcome domain.WithdrawOutcome
	err = database.Transact(db, func(tx database.Tx) error {
		if err := tx.DB().Exec(advisoryLockSQL, userID).Error; err != nil {
			return fmt.Errorf("lock withdrawal balance: %w", err)
		}

		insert := tx.DB().Exec(withdrawSQL, userID, number, sum, userID, userID, sum)
		if insert.Error != nil {
			return fmt.Errorf("insert withdrawal: %w", insert.Error)
		}
		if insert.RowsAffected == 1 {
			outcome = domain.Withdrawn

			return nil
		}

		var err error
		outcome, err = findWithdrawalOwner(tx, number, userID)

		return err
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}

	return outcome, nil
}

func findWithdrawalOwner(
	tx database.Tx,
	number string,
	userID int64,
) (domain.WithdrawOutcome, error) {
	var stored withdrawalOwnerRow
	err := tx.DB().Table("withdrawals").
		Select("user_id").
		Where("order_number = ?", number).
		Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NotEnoughFunds, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find withdrawal owner: %w", err)
	}
	if stored.UserID == userID {
		return domain.AlreadyWithdrawn, nil
	}

	return domain.TakenByAnother, nil
}
