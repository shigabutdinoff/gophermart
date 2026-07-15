package server

import (
	"github.com/go-chi/chi/v5"

	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/compress"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/logging"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/recovery"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/healthcheck"
)

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	r.Use(logging.WithLogging(s.logger))
	r.Use(recovery.WithRecovery(s.logger))
	r.Use(compress.GzipMiddleware)

	r.Get("/ping", healthcheck.Ping)

	s.router = r
}
