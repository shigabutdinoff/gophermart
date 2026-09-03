package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrUnavailable означает, что подключение к базе данных не открыто.
var ErrUnavailable = errors.New("database is unavailable")

// Config собирает настройки gorm, общие для боевого подключения и фикстур.
func Config() *gorm.Config {
	return &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
		TranslateError:       true,
	}
}

// Open создаёт ленивое подключение к PostgreSQL.
func Open(dsn string) (Session, error) {
	if dsn == "" {
		return Session{}, errors.New("не задан адрес подключения к БД")
	}

	db, err := gorm.Open(postgres.Open(dsn), Config())
	if err != nil {
		if _, ok := errors.AsType[*pgconn.ParseConfigError](err); ok {
			return Session{}, errors.New("некорректная строка подключения к БД")
		}
		return Session{}, err
	}

	return NewSession(db), nil
}
