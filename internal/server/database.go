package server

import (
	"context"
	"database/sql"

	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

// initDatabase подключает БД и проверяет соединение.
func (s *Server) initDatabase(ctx context.Context) {
	db, err := database.Connection(s.DatabaseURI)
	if err != nil {
		s.logger.Warn("Не удалось открыть соединение с БД", zap.Error(err))
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		s.logger.Warn("Не удалось получить пул соединений БД", zap.Error(err))
		return
	}
	s.swapDatabase(sqlDB)

	if err := sqlDB.PingContext(ctx); err != nil {
		s.logger.Warn("БД недоступна", zap.Error(err))
	}
}

func (s *Server) closeDatabase() {
	db := s.swapDatabase(nil)
	if db == nil {
		return
	}

	if err := db.Close(); err != nil {
		s.logger.Warn("Не удалось закрыть соединение с БД", zap.Error(err))
	}
}

func (s *Server) swapDatabase(db *sql.DB) *sql.DB {
	s.dbMu.Lock()
	previous := s.db
	s.db = db
	s.dbMu.Unlock()
	return previous
}

func (s *Server) currentDatabase() *sql.DB {
	s.dbMu.RLock()
	db := s.db
	s.dbMu.RUnlock()
	return db
}
