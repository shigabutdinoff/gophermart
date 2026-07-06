package server

import (
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

const DefaultRunAddress = "localhost:8080"

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
