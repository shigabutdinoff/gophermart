package server

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/route/healthcheck"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

const (
	databaseMaxConns        = 10
	databaseConnMaxIdleTime = time.Minute
)

// Бюджет подключения к БД при старте с паузами между попытками
const (
	databaseConnectAttempts = 3
	databaseConnectBudget   = 5 * time.Second
	// попытка короче секунды, иначе третья не успевает начаться в бюджете
	databasePingTimeout = 700 * time.Millisecond
)

func (s *Server) pinger() healthcheck.Pinger {
	if s.sqlDB == nil {
		return unavailableDB{}
	}
	return s.sqlDB
}

type unavailableDB struct{}

func (unavailableDB) PingContext(context.Context) error {
	return database.ErrUnavailable
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
) error {
	if s.sqlDB == nil {
		return database.ErrUnavailable
	}

	if err := s.pingWithRetry(ctx); err != nil {
		s.logger.Error("БД недоступна, миграции пропущены", zap.Error(err))
		return err
	}

	if err := migrate(ctx, s.sqlDB); err != nil {
		s.logger.Error("Не удалось применить миграции", zap.Error(err))
		return err
	}

	s.logger.Info("Миграции выполнены")
	return nil
}

func (s *Server) databaseLifecycle(ctx context.Context, migrate func(context.Context, *sql.DB) error) (<-chan struct{}, func() error, func(error)) {
	ready := make(chan struct{})
	if err := s.initDatabase(ctx, migrate); err == nil {
		close(ready)
		return ready, nil, nil
	}
	if s.sqlDB == nil {
		return ready, nil, nil
	}
	actor, interrupt := s.databaseRecoveryActor(ctx, ready, migrate)
	return ready, actor, interrupt
}

func (s *Server) databaseRecoveryActor(ctx context.Context, ready chan<- struct{}, migrate func(context.Context, *sql.DB) error) (func() error, func(error)) {
	if s.sqlDB == nil {
		return nil, nil
	}
	recoveryCtx, cancelRecovery := context.WithCancel(ctx)
	var interruptOnce sync.Once
	return func() error {
		err := retry.Do(
			func() error { return s.initDatabase(recoveryCtx, migrate) },
			s.retryForeverOptions(recoveryCtx, func(attempt uint, err error) {
				s.logger.Warn("Повторная попытка подготовить БД", zap.Uint("attempt", attempt+1), zap.Error(err))
			})...,
		)
		if err != nil {
			return nil
		}
		close(ready)
		<-recoveryCtx.Done()
		return nil
	}, func(error) { interruptOnce.Do(cancelRecovery) }
}

// pingWithRetry повторяет проверку связи с БД в пределах общего бюджета.
func (s *Server) pingWithRetry(ctx context.Context) error {
	budgetCtx, cancel := context.WithTimeout(ctx, databaseConnectBudget)
	defer cancel()

	return retry.Do(
		func() error {
			attemptCtx, cancelAttempt := context.WithTimeout(budgetCtx, databasePingTimeout)
			defer cancelAttempt()

			return s.sqlDB.PingContext(attemptCtx)
		},
		retry.Context(budgetCtx),
		retry.Attempts(databaseConnectAttempts),
		retry.Delay(s.retryDelay),
		retry.DelayType(retry.FixedDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(attempt uint, err error) {
			s.logger.Warn(
				"Повторная попытка подключения к БД",
				zap.Uint("attempt", attempt+1),
				zap.Error(err),
			)
		}),
	)
}

func (s *Server) closeDatabase() {
	closeDatabaseHandle(s.logger, s.sqlDB)
}

func (s *Server) closeDatabaseBeforeDeadline(ctx context.Context) error {
	if s.sqlDB == nil {
		return nil
	}

	return closeDatabaseWithin(ctx, s.logger, s.sqlDB.Close)
}

// closeDatabaseWithin начинает Close в любом случае, но ждёт его только
// в пределах общего shutdown context.
func closeDatabaseWithin(
	ctx context.Context,
	logger *zap.Logger,
	closeDatabase func() error,
) error {
	done := make(chan error, 1)
	go func() {
		done <- closeDatabase()
	}()

	select {
	case err := <-done:
		if err != nil {
			logger.Warn("Не удалось закрыть соединение с БД", zap.Error(err))
		}
		return err
	case <-ctx.Done():
		err := context.Cause(ctx)
		logger.Warn("Не удалось завершить закрытие соединения с БД", zap.Error(err))
		return err
	}
}

// closeDatabaseHandle закрывает пул и до появления сервера, и вместе с ним.
func closeDatabaseHandle(logger *zap.Logger, sqlDB *sql.DB) {
	if sqlDB == nil {
		return
	}

	if err := sqlDB.Close(); err != nil {
		logger.Warn("Не удалось закрыть соединение с БД", zap.Error(err))
	}
}
