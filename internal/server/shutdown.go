package server

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// shutdownBudget лениво заводит единый абсолютный срок остановки.
type shutdownBudget struct {
	timeout    time.Duration
	once       sync.Once
	ctx        context.Context
	cancel     context.CancelFunc
	httpCtx    context.Context
	httpCancel context.CancelFunc
}

func newShutdownBudget(timeout time.Duration) *shutdownBudget {
	return &shutdownBudget{timeout: timeout}
}

func (b *shutdownBudget) start() {
	b.once.Do(func() {
		startedAt := time.Now()
		b.ctx, b.cancel = context.WithDeadline(context.Background(), startedAt.Add(b.timeout))
		b.httpCtx, b.httpCancel = context.WithDeadline(b.ctx, startedAt.Add(b.timeout/2))
	})
}

// context возвращает единый абсолютный срок всей остановки.
func (b *shutdownBudget) context() context.Context {
	b.start()

	return b.ctx
}

// httpContext отдаёт HTTP первую половину общего срока.
func (b *shutdownBudget) httpContext() context.Context {
	b.start()

	return b.httpCtx
}

func (b *shutdownBudget) release() {
	if b.httpCancel != nil {
		b.httpCancel()
	}
	if b.cancel != nil {
		b.cancel()
	}
}

// shutdown останавливает сервер, при таймауте закрывает принудительно.
func (s *Server) shutdown(ctx context.Context) error {
	s.logger.Info("Начата остановка сервера")

	if err := s.srv.Shutdown(ctx); err != nil {
		s.logger.Info("Превышен таймаут остановки, принудительное закрытие")
		_ = s.srv.Close()
		return fmt.Errorf("server shutdown: %w", err)
	}

	s.logger.Info("Остановка завершена")
	return nil
}
