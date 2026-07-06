package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// shutdown останавливает сервер, при таймауте закрывает принудительно.
func (s *Server) shutdown(errCh <-chan error) error {
	s.logger.Info("Начата остановка сервера")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Info("Превышен таймаут остановки, принудительное закрытие")
		_ = s.srv.Close()
		return fmt.Errorf("server shutdown: %w", err)
	}

	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	s.logger.Info("Остановка завершена")
	return nil
}
