package server

import (
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

const (
	DefaultShutdownTimeout   = 10 * time.Second
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
)

// Server запускает HTTP-сервер и останавливает его (graceful shutdown).
type Server struct {
	router          *chi.Mux
	logger          *zap.Logger
	shutdownTimeout time.Duration
	ln              net.Listener
	srv             *http.Server
	config.Config
}

// New создаёт сервер с переданной конфигурацией.
func New(logger *zap.Logger, cfg config.Config) *Server {
	s := &Server{
		logger:          logger,
		shutdownTimeout: DefaultShutdownTimeout,
		Config:          cfg,
	}
	s.setupRoutes()
	return s
}
