package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connection создаёт ленивое подключение к PostgreSQL.
func Connection(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, errors.New("не задан адрес подключения к БД")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		var parseErr *pgconn.ParseConfigError
		if errors.As(err, &parseErr) {
			return nil, errors.New("некорректная строка подключения к БД")
		}
		return nil, err
	}

	return db, nil
}
