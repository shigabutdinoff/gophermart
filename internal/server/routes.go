package server

import (
	"github.com/go-chi/chi/v5"

	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/logging"
)

func (s *Server) setupRoutes() {
	router := chi.NewRouter()

	router.Use(logging.WithLogging(s.logger))
	router.Post("/api/user/register", s.register)
	router.Post("/api/user/login", s.login)

	s.router = router
}
