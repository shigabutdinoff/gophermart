package user

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const usersTable = "users"

// userRow задаёт колонки явно, домен не зависит от соглашений gorm об именах.
type userRow struct {
	ID           int64     `gorm:"column:id;primaryKey"`
	Login        string    `gorm:"column:login"`
	PasswordHash string    `gorm:"column:password_hash"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (r userRow) user() auth.User {
	return auth.User{
		ID:           r.ID,
		Login:        r.Login,
		PasswordHash: r.PasswordHash,
		CreatedAt:    r.CreatedAt,
	}
}

// Repository хранит учётные записи, логин ожидается уже нормализованным.
type Repository struct {
	db *gorm.DB
}

// New принимает nil вместо БД, тогда репозиторий отвечает отказом.
func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// session привязывает подключение к запросу, отсутствие БД означает отказ.
func (r *Repository) session(ctx context.Context) (*gorm.DB, error) {
	if r.db == nil {
		return nil, database.ErrUnavailable
	}
	return r.db.WithContext(ctx), nil
}

func (r *Repository) Create(
	ctx context.Context,
	login string,
	passwordHash string,
) (auth.User, error) {
	db, err := r.session(ctx)
	if err != nil {
		return auth.User{}, err
	}

	row := userRow{Login: login, PasswordHash: passwordHash}
	switch err := db.Table(usersTable).Create(&row).Error; {
	case err == nil:
		return row.user(), nil
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return auth.User{}, auth.ErrLoginTaken
	default:
		return auth.User{}, err
	}
}

func (r *Repository) FindByLogin(ctx context.Context, login string) (auth.User, error) {
	db, err := r.session(ctx)
	if err != nil {
		return auth.User{}, err
	}

	var row userRow
	switch err := db.Table(usersTable).Where("login = ?", login).Take(&row).Error; {
	case err == nil:
		return row.user(), nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return auth.User{}, auth.ErrUserNotFound
	default:
		return auth.User{}, err
	}
}
