package database

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// Session даёт репозиториям одно подключение и один способ отказать без БД.
type Session struct {
	db *gorm.DB
}

// NewSession принимает nil вместо БД, тогда репозиторий отвечает отказом.
func NewSession(db *gorm.DB) Session {
	return Session{db: db}
}

// WithContext привязывает подключение к запросу.
func (s Session) WithContext(ctx context.Context) (*gorm.DB, error) {
	if s.db == nil {
		return nil, ErrUnavailable
	}
	return s.db.WithContext(ctx), nil
}

// Pool отдаёт пул соединений тем, кто работает мимо gorm.
func (s Session) Pool() (*sql.DB, error) {
	if s.db == nil {
		return nil, ErrUnavailable
	}
	return s.db.DB()
}
