package server

import (
	"context"
	"fmt"
)

// shutdown останавливает сервер, при таймауте закрывает принудительно.
func (s *Server) shutdown() error {
	s.logger.Info("Начата остановка сервера")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Info("Превышен таймаут остановки, принудительное закрытие")
		_ = s.srv.Close()
		return fmt.Errorf("server shutdown: %w", err)
	}

	s.logger.Info("Остановка завершена")
	return nil
}
