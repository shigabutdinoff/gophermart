package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/oklog/run"
	"go.uber.org/zap"
)

// retryMaxDelay ограничивает рост паузы между попытками восстановиться
const retryMaxDelay = 30 * time.Second

// Run работает до отмены контекста, затем останавливается за shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	ready, recoverDatabase, interruptDatabaseRecovery := s.databaseLifecycle(ctx, s.migrateDatabase)
	if interruptDatabaseRecovery != nil {
		defer interruptDatabaseRecovery(nil)
	}

	budget := newShutdownBudget(s.shutdownTimeout)
	defer budget.release()
	// свёртка идёт отложенно: ранний отказ запуска тоже гасит очередь и БД
	defer func() {
		shutdownCtx := budget.context()
		s.stopQueue(shutdownCtx)
		_ = s.closeDatabaseBeforeDeadline(shutdownCtx)
	}()

	if s.ln == nil {
		if err := s.listen(); err != nil {
			return err
		}
	}

	srv, err := s.newHTTPServer()
	if err != nil {
		_ = s.ln.Close()
		return err
	}
	s.srv = srv

	// пишется и читается в горутине, вызвавшей Run, гонки здесь нет
	var shutdownErr error

	var group run.Group
	group.Add(func() error {
		if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}, func(error) {
		if ctx.Err() != nil {
			shutdownErr = s.shutdown(budget.httpContext())
			return
		}
		// обслуживание прервалось само, соединения закрываются принудительно
		_ = s.srv.Close()
	})
	if recoverDatabase != nil {
		group.Add(recoverDatabase, interruptDatabaseRecovery)
	}
	if s.deps.runner != nil {
		group.Add(s.queueActor(ctx, ready))
	}
	group.Add(run.ContextHandler(ctx))
	s.logger.Info("Сервер запущен", zap.String("address", s.Addr()))

	err = group.Run()
	if errors.Is(err, context.Canceled) {
		// отмена контекста означает штатную остановку, а не ошибку запуска
		err = nil
	}

	if shutdownErr != nil {
		s.logger.Error("Не удалось остановить обслуживание", zap.Error(shutdownErr))
	}

	// неудачи остановки остаются в логе: процесс уже сворачивается, и код
	// выхода не должен отличать штатный сигнал от затянувшегося дренажа
	return err
}

// stopQueue сворачивает очередь в остатке единого срока остановки.
func (s *Server) stopQueue(ctx context.Context) {
	if s.deps.runner == nil {
		return
	}

	if err := s.deps.runner.Stop(ctx); err != nil {
		s.logger.Error("Не удалось остановить очередь заданий", zap.Error(err))
	}
}

// queueActor держит очередь заданий поднятой, пока её не остановят.
// Сервис работает и без опроса расчёта, поэтому отказ очереди его не роняет.
func (s *Server) queueActor(
	ctx context.Context,
	ready <-chan struct{},
) (func() error, func(error)) {
	interrupted := make(chan struct{})
	retryCtx, cancelRetry := context.WithCancel(context.WithoutCancel(ctx))
	var interruptOnce sync.Once

	return func() error {
			defer cancelRetry()

			select {
			case <-ready:
			case <-interrupted:
				return nil
			}

			s.keepQueueRunning(retryCtx, interrupted)
			// отказ очереди сервису не приговор: актёр держит группу до
			// прерывания, а не сворачивает обслуживание вместе с собой
			<-interrupted

			return nil
		}, func(error) {
			interruptOnce.Do(func() {
				close(interrupted)
				cancelRetry()
				s.deps.runner.CancelStart()
			})
		}
}

// keepQueueRunning поднимает очередь заново и после остановки на ходу: без
// опроса заказы иначе ждали бы расчёта до перезапуска процесса.
func (s *Server) keepQueueRunning(ctx context.Context, interrupted <-chan struct{}) {
	for {
		// повторы кончаются только прерыванием: отказ подъёма ждёт
		// следующей попытки, а не отчёта об ошибке
		if err := s.startQueue(ctx); err != nil {
			return
		}
		s.resumePendingOrders(ctx)

		select {
		case <-s.deps.runner.Stopped():
		case <-interrupted:
			return
		}
		select {
		case <-interrupted:
			return
		default:
		}

		s.logger.Error("Очередь заданий неожиданно остановилась")
		if !s.waitBeforeRestart(interrupted) {
			return
		}
	}
}

// resumePendingOrders возвращает в опрос заказы, чьи задания были потеряны.
// Ошибка не мешает уже поднятой очереди обслуживать остальные задания.
func (s *Server) resumePendingOrders(ctx context.Context) {
	if s.deps.pendingOrderResumer == nil {
		return
	}

	inserted, err := s.deps.pendingOrderResumer.Resume(ctx)
	if err != nil {
		s.logger.Warn(
			"Не удалось вернуть незакрытые заказы в очередь",
			zap.Int("count", inserted),
			zap.Error(err),
		)

		return
	}

	s.logger.Info(
		"Незакрытые заказы возвращены в очередь",
		zap.Int("count", inserted),
	)
}

// waitBeforeRestart отделяет попытки подъёма: очередь, падающая сразу после
// старта, иначе крутила бы цикл вхолостую.
func (s *Server) waitBeforeRestart(interrupted <-chan struct{}) bool {
	timer := time.NewTimer(s.retryDelay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-interrupted:
		return false
	}
}

// startQueue повторяет подъём очереди, пока её не прервут: недоступная на
// старте БД иначе оставила бы заказы неопрошенными до перезапуска процесса.
func (s *Server) startQueue(ctx context.Context) error {
	return retry.Do(
		func() error {
			err := s.deps.runner.Start(ctx)
			if err != nil && context.Cause(ctx) != nil {
				return retry.Unrecoverable(context.Cause(ctx))
			}

			return err
		},
		s.retryForeverOptions(ctx, func(attempt uint, err error) {
			s.logger.Warn(
				"Повторная попытка запустить очередь заданий",
				zap.Uint("attempt", attempt+1),
				zap.Error(err),
			)
		})...,
	)
}

// retryForeverOptions повторяет попытки до прерывания с растущей паузой.
func (s *Server) retryForeverOptions(
	ctx context.Context,
	onRetry retry.OnRetryFunc,
) []retry.Option {
	return []retry.Option{
		retry.Context(ctx),
		retry.Attempts(0),
		retry.Delay(s.retryDelay),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxDelay(retryMaxDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(onRetry),
	}
}
