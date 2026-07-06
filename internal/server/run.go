package server

import (
	"context"

	"go.uber.org/zap"
)

// Run работает до отмены контекста, затем останавливается за shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.srv.Serve(s.ln)
	}()
	s.logger.Info("Сервер запущен", zap.String("address", s.Addr()))

	select {
	case err := <-errCh:
		_ = s.srv.Close()
		return err
	case <-ctx.Done():
	}

	return s.shutdown(errCh)
}
