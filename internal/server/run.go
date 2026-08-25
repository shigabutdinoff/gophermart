package server

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/oklog/run"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/repository/database"
)

// Run работает до отмены контекста, затем останавливается за shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	s.initDatabase(ctx, database.Migrate)
	defer s.closeDatabase()

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
	budget := newShutdownBudget(s.shutdownTimeout)
	defer budget.release()

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
	if s.deps.runner != nil {
		group.Add(s.queueActor(ctx))
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
	s.stopQueue(budget.context())

	// неудачи остановки остаются в логе: процесс уже сворачивается, и код
	// выхода не должен отличать штатный сигнал от затянувшегося дренажа
	return err
}

// queueActor запускает очередь один раз и держит её в группе до остановки.
// Ошибка очереди не останавливает HTTP-сервис.
func (s *Server) queueActor(ctx context.Context) (func() error, func(error)) {
	interrupted := make(chan struct{})
	startCtx, cancelStart := context.WithCancel(context.WithoutCancel(ctx))
	var interruptOnce sync.Once

	return func() error {
			if err := s.deps.runner.Start(startCtx); err != nil {
				s.logger.Error("Не удалось запустить очередь заданий", zap.Error(err))
			}
			<-interrupted

			return nil
		}, func(error) {
			interruptOnce.Do(func() {
				close(interrupted)
				cancelStart()
			})
		}
}

func (s *Server) stopQueue(ctx context.Context) {
	if s.deps.runner == nil {
		return
	}

	if err := s.deps.runner.Stop(ctx); err != nil {
		s.logger.Error("Не удалось остановить очередь заданий", zap.Error(err))
	}
}
