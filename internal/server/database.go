package server

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/route/healthcheck"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const (
	databaseMaxConns        = 10
	databaseConnMaxIdleTime = time.Minute
)

func (s *Server) pinger() healthcheck.Pinger {
	if s.sqlDB == nil {
		return nil
	}
	return s.sqlDB
}

func openDatabase(logger *zap.Logger, dsn string) (database.Session, *sql.DB) {
	session, err := database.Open(dsn)
	if err != nil {
		logger.Warn("Не удалось открыть соединение с БД", zap.Error(err))
		return database.Session{}, nil
	}

	sqlDB, err := session.Pool()
	if err != nil {
		logger.Warn("Не удалось получить пул соединений БД", zap.Error(err))
		return database.Session{}, nil
	}
	sqlDB.SetMaxOpenConns(databaseMaxConns)
	sqlDB.SetMaxIdleConns(databaseMaxConns)
	sqlDB.SetConnMaxIdleTime(databaseConnMaxIdleTime)

	return session, sqlDB
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
