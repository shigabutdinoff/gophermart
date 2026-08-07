package server

import (
	"database/sql"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

const DefaultShutdownTimeout = 10 * time.Second

type Server struct {
	router          *chi.Mux
	logger          *zap.Logger
	shutdownTimeout time.Duration
	ln              net.Listener
	srv             *http.Server
	sqlDB           *sql.DB
	config.Config
}

func New(logger *zap.Logger, cfg config.Config) (*Server, error) {
	return newServer(logger, cfg, openDatabase(logger, cfg.DatabaseURI))
}

func newServer(logger *zap.Logger, cfg config.Config, sqlDB *sql.DB) (*Server, error) {
	server := &Server{
		logger:          logger,
		shutdownTimeout: DefaultShutdownTimeout,
		sqlDB:           sqlDB,
		Config:          cfg,
	}
	server.setupRoutes()
	return server, nil
}
