package order

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	domain "github.com/shigabutdinoff/gophermart/internal/order"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const ordersTable = "orders"

// orderRow задаёт колонки явно, домен не зависит от соглашений gorm об именах.
type orderRow struct {
	ID         int64         `gorm:"column:id;primaryKey"`
	Number     string        `gorm:"column:number"`
	UserID     int64         `gorm:"column:user_id"`
	Status     domain.Status `gorm:"column:status"`
	UploadedAt time.Time     `gorm:"column:uploaded_at;autoCreateTime"`
}

func (r orderRow) order() domain.Order {
	return domain.Order{
		Number:     r.Number,
		UserID:     r.UserID,
		Status:     r.Status,
		UploadedAt: r.UploadedAt,
	}
}

// Repository хранит заказы, номер ожидается уже нормализованным.
type Repository struct {
	session database.Session
}

// New принимает nil вместо БД, тогда репозиторий отвечает отказом.
func New(db *gorm.DB) *Repository {
	return &Repository{session: database.NewSession(db)}
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

	var outcome domain.CreateOutcome
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		outcome, err = createOrFindOwner(tx, number, userID)

		return err
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
