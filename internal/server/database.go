package server

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const (
	databaseMaxConns        = 10
	databaseConnMaxIdleTime = time.Minute
)

func openDatabase(logger *zap.Logger, dsn string) (*gorm.DB, *sql.DB) {
	gormDB, err := database.Connection(dsn)
	if err != nil {
		logger.Warn("Не удалось открыть соединение с БД", zap.Error(err))
		return nil, nil
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		logger.Warn("Не удалось получить пул соединений БД", zap.Error(err))
		return nil, nil
	}
	sqlDB.SetMaxOpenConns(databaseMaxConns)
	sqlDB.SetMaxIdleConns(databaseMaxConns)
	sqlDB.SetConnMaxIdleTime(databaseConnMaxIdleTime)

	return gormDB, sqlDB
}

func (s *Server) initDatabase(
	ctx context.Context,
	migrate func(context.Context, *sql.DB) error,
) {
	if s.sqlDB == nil {
		return
	}

	if err := s.sqlDB.PingContext(ctx); err != nil {
		s.logger.Error("БД недоступна, миграции пропущены", zap.Error(err))
		return
	}

	if err := migrate(ctx, s.sqlDB); err != nil {
		s.logger.Error("Не удалось применить миграции", zap.Error(err))
		return
	}

	s.logger.Info("Миграции выполнены")
}

func (s *Server) closeDatabase() {
	closeDatabaseHandle(s.logger, s.sqlDB)
}

func closeDatabaseHandle(logger *zap.Logger, sqlDB *sql.DB) {
	if sqlDB == nil {
		return
	}

	if err := sqlDB.Close(); err != nil {
		logger.Warn("Не удалось закрыть соединение с БД", zap.Error(err))
	}
}
