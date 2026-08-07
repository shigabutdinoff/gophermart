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

// Connection создаёт ленивое подключение к PostgreSQL.
func Connection(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, errors.New("не задан адрес подключения к БД")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
		TranslateError:       true,
	})
	if err != nil {
		if _, ok := errors.AsType[*pgconn.ParseConfigError](err); ok {
			return nil, errors.New("некорректная строка подключения к БД")
		}
		return nil, err
	}

	return db, nil
}
