package server

import (
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

const (
	DefaultRunAddress        = "localhost:8080"
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
)

// Server запускает HTTP-сервер.
type Server struct {
	router     *chi.Mux
	logger     *zap.Logger
	RunAddress string
	ln         net.Listener
	srv        *http.Server
}

// New создаёт сервер с параметрами по умолчанию.
func New(logger *zap.Logger) *Server {
	s := &Server{
		logger:     logger,
		RunAddress: DefaultRunAddress,
	}
	s.setupRoutes()
	return s
}
