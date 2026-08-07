package server

import (
	"context"
	"database/sql"

	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

func openDatabase(logger *zap.Logger, dsn string) *sql.DB {
	gormDB, err := database.Connection(dsn)
	if err != nil {
		logger.Warn("Не удалось открыть соединение с БД", zap.Error(err))
		return nil
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		logger.Warn("Не удалось получить пул соединений БД", zap.Error(err))
		return nil
	}
	return sqlDB
}

func (s *Server) initDatabase(ctx context.Context) {
	s.initDatabaseWith(ctx, database.Migrate)
}

func (s *Server) initDatabaseWith(
	ctx context.Context,
	migrate func(context.Context, *sql.DB) error,
) {
	if s.sqlDB == nil {
		return
	}

	s.checkDatabaseAndMigrate(ctx, s.sqlDB, migrate)
}

func (s *Server) checkDatabaseAndMigrate(
	ctx context.Context,
	db *sql.DB,
	migrate func(context.Context, *sql.DB) error,
) {
	if err := db.PingContext(ctx); err != nil {
		s.logger.Error("БД недоступна, миграции пропущены", zap.Error(err))
		return
	}

	if err := migrate(ctx, db); err != nil {
		s.logger.Error("Не удалось применить миграции", zap.Error(err))
		return
	}

	s.logger.Info("Миграции выполнены")
}

func (s *Server) closeDatabase() {
	if s.sqlDB == nil {
		return
	}

	if err := s.sqlDB.Close(); err != nil {
		s.logger.Warn("Не удалось закрыть соединение с БД", zap.Error(err))
	}
}
