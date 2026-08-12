package server

import (
	"context"
	"errors"
	"net/http"

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

	var group run.Group
	group.Add(func() error {
		if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}, func(error) {
		if ctx.Err() != nil {
			shutdownErr = s.shutdown()
			return
		}
		// обслуживание прервалось само, соединения закрываются принудительно
		_ = s.srv.Close()
	})
	group.Add(run.ContextHandler(ctx))
	s.logger.Info("Сервер запущен", zap.String("address", s.Addr()))

	err = group.Run()
	if errors.Is(err, context.Canceled) {
		// отмена контекста означает штатную остановку, а не ошибку запуска
		err = nil
	}

	return errors.Join(err, shutdownErr)
}
