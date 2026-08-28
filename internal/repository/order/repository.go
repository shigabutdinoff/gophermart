package order

import (
	"context"
	"database/sql"
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

// Pusher ставит задание на опрос расчёта в переданную транзакцию.
type Pusher interface {
	Push(ctx context.Context, tx *sql.Tx, number string) error
}

// Repository хранит заказы, номер ожидается уже нормализованным.
type Repository struct {
	session database.Session
	jobs    Pusher
}

// New принимает нулевую сессию вместо БД, тогда репозиторий отвечает отказом.
func New(session database.Session) *Repository {
	return &Repository{session: session}
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
	err = database.Transact(db, func(tx database.Tx) error {
		var err error
		outcome, err = createOrFindOwner(tx.DB(), number, userID)
		if err != nil || outcome != domain.Created {
			return err
		}
		sqlTx, err := tx.SQL()
		if err != nil {
			return err
		}
		if err := r.jobs.Push(ctx, sqlTx, number); err != nil {
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
		// строку только что перехватил конкурент и она уже удалена
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = domain.ErrNotFound
		}

		return 0, fmt.Errorf("find order owner: %w", err)
	}
	if stored.UserID == userID {
		return domain.AlreadyOwned, nil
	}

	return domain.OwnedByAnother, nil
}

// ApplyResult пишет исход расчёта и отвечает, закончен ли заказ.
// Завершённый заказ переписывать нельзя, и решает это сама запись: условие
// внутри неё заменяет блокировку, а RETURNING отличает заказ, закрытый до нас,
// от пропавшей строки, на которую ушло бы ещё одно задание.
func (r *Repository) ApplyResult(
	ctx context.Context,
	number string,
	status domain.Status,
	accrual *money.Points,
) (bool, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return false, err
	}

	final := domain.FinalStatuses()
	var updated orderRow
	update := db.Table(ordersTable).
		Model(&updated).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "status"}}}).
		Where("number = ?", number).
		Updates(map[string]any{
			"status":  gorm.Expr("CASE WHEN status IN ? THEN status ELSE ? END", final, status),
			"accrual": gorm.Expr("CASE WHEN status IN ? THEN accrual ELSE ? END", final, accrual),
		})
	if update.Error != nil {
		return false, fmt.Errorf("apply order result: %w", update.Error)
	}
	// пропавшую строку не вернёт ни одна следующая попытка записи
	if update.RowsAffected == 0 {
		return false, fmt.Errorf("apply order result: %w", domain.ErrNotFound)
	}

	return updated.Status.IsFinal(), nil
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

// ListUnfinished отдаёт номера заказов, ещё не дошедших до финального статуса.
func (r *Repository) ListUnfinished(ctx context.Context) ([]string, error) {
	db, err := r.session.WithContext(ctx)
	if err != nil {
		return nil, err
	}

	var numbers []string
	if err := db.Table(ordersTable).
		Select("number").
		Where("status NOT IN ?", domain.FinalStatuses()).
		Find(&numbers).Error; err != nil {
		return nil, err
	}

	return numbers, nil
}
