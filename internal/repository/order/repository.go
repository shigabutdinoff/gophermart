package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/shigabutdinoff/gophermart/internal/money"
	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const ordersTable = "orders"

// errPusherMissing отвечает за репозиторий, собранный без очереди заданий.
var errPusherMissing = errors.New("order job pusher is not configured")

// orderRow задаёт колонки явно, домен не зависит от соглашений gorm об именах.
type orderRow struct {
	ID         int64         `gorm:"column:id;primaryKey"`
	Number     string        `gorm:"column:number"`
	UserID     int64         `gorm:"column:user_id"`
	Status     domain.Status `gorm:"column:status"`
	UploadedAt time.Time     `gorm:"column:uploaded_at;autoCreateTime"`
	Accrual    *money.Points `gorm:"column:accrual"`
}

func (r orderRow) order() domain.Order {
	return domain.Order{
		Number:     r.Number,
		UserID:     r.UserID,
		Status:     r.Status,
		UploadedAt: r.UploadedAt,
		Accrual:    r.Accrual,
	}
}

// Pusher ставит задание на опрос расчёта в очередь.
type Pusher interface {
	Push(ctx context.Context, tx *gorm.DB, number string) error
}

// Repository хранит заказы, номер ожидается уже нормализованным.
type Repository struct {
	session database.Session
	jobs    Pusher
}

// New принимает nil вместо БД, тогда репозиторий отвечает отказом.
func New(db *gorm.DB) *Repository {
	return &Repository{session: database.NewSession(db)}
}

// AttachPusher один раз подключает очередь после сборки её клиента.
func (r *Repository) AttachPusher(jobs Pusher) error {
	if jobs == nil {
		return errors.New("cannot attach nil order job pusher")
	}
	if r.jobs != nil {
		return errors.New("order job pusher is already configured")
	}

	r.jobs = jobs

	return nil
}

// CreateOrFindOwner классифицирует создание или конфликт номера.
func (r *Repository) CreateOrFindOwner(
	ctx context.Context,
	number string,
	userID int64,
) (domain.CreateOutcome, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return 0, err
	}
	if r.jobs == nil {
		return 0, errPusherMissing
	}

	var outcome domain.CreateOutcome
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		outcome, err = createOrFindOwner(tx, number, userID)
		if err != nil || outcome != domain.Created {
			return err
		}
		if err := r.jobs.Push(ctx, tx, number); err != nil {
			return fmt.Errorf("dispatch accrual check: %w", err)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return outcome, nil
}

// createOrFindOwner молчит о конфликте номера, прочие остаются ошибкой.
func createOrFindOwner(tx *gorm.DB, number string, userID int64) (domain.CreateOutcome, error) {
	row := orderRow{
		Number: number,
		UserID: userID,
		Status: domain.StatusNew,
	}
	insert := tx.Table(ordersTable).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "number"}},
			DoNothing: true,
		}).
		Create(&row)
	if insert.Error != nil {
		return 0, insert.Error
	}
	if insert.RowsAffected == 1 {
		return domain.Created, nil
	}

	var stored orderRow
	if err := tx.Table(ordersTable).
		Select("user_id").
		Where("number = ?", number).
		Take(&stored).Error; err != nil {
		return 0, fmt.Errorf("find order owner: %w", err)
	}
	if stored.UserID == userID {
		return domain.AlreadyOwned, nil
	}

	return domain.OwnedByAnother, nil
}

func (r *Repository) ListByUser(ctx context.Context, userID int64) ([]domain.Order, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return nil, err
	}

	var rows []orderRow
	if err := db.Table(ordersTable).
		Where("user_id = ?", userID).
		Order("uploaded_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	orders := make([]domain.Order, len(rows))
	for i, row := range rows {
		orders[i] = row.order()
	}

	return orders, nil
}
