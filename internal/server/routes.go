package server

import (
	"github.com/go-chi/chi/v5"

	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/logging"
)

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	r.Use(logging.WithLogging(s.logger))

	s.router = r
}
