package server

import (
	"context"
	"database/sql"

	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

// initDatabase подключает БД и запускает миграции.
func (s *Server) initDatabase(ctx context.Context) {
	s.initDatabaseWith(ctx, database.Migrate)
}

func (s *Server) initDatabaseWith(
	ctx context.Context,
	migrate func(string, string) (bool, error),
) {
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
	s.checkDatabaseAndMigrate(ctx, sqlDB, migrate)
}

func (s *Server) checkDatabaseAndMigrate(
	ctx context.Context,
	pinger interface{ PingContext(context.Context) error },
	migrate func(string, string) (bool, error),
) {
	if err := pinger.PingContext(ctx); err != nil {
		s.logger.Warn("БД недоступна, миграции пропущены", zap.Error(err))
		return
	}

	applied, err := migrate(database.MigrationsURL, s.DatabaseURI)
	switch {
	case err != nil:
		s.logger.Warn("Не удалось применить миграции", zap.Error(err))
	case applied:
		s.logger.Info("Миграции применены")
	default:
		s.logger.Info("Миграции: нет изменений")
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
