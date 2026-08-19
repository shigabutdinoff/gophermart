package server

import (
	"database/sql"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
	userrepository "github.com/shigabutdinoff/gophermart/internal/repository/user"
)

const DefaultShutdownTimeout = 10 * time.Second

type Server struct {
	router          *chi.Mux
	logger          *zap.Logger
	shutdownTimeout time.Duration
	ln              net.Listener
	srv             *http.Server
	sqlDB           *sql.DB
	authDeps        authentication.Deps
	authorize       func(http.Handler) http.Handler
	config.Config
}

func New(logger *zap.Logger, cfg config.Config) (*Server, error) {
	gormDB, sqlDB := openDatabase(logger, cfg.DatabaseURI)
	server, err := newServer(logger, cfg, time.Now, gormDB, sqlDB)
	if err != nil {
		closeDatabaseHandle(logger, sqlDB)
	}

	return server, err
}

func newServer(
	logger *zap.Logger,
	cfg config.Config,
	now auth.Clock,
	gormDB *gorm.DB,
	sqlDB *sql.DB,
) (*Server, error) {
	secret, err := auth.ResolveSecret(cfg.JWTSecret)
	if err != nil {
		return nil, err
	}
	tokens, err := auth.NewJWTManager(secret, now)
	if err != nil {
		return nil, err
	}
	cfg.JWTSecret = ""

	users := userrepository.New(gormDB)
	passwords := auth.Argon2Passwords{}
	registration := auth.NewRegisterService(users, passwords, tokens)
	login, err := auth.NewLoginService(logger, users, passwords, tokens)
	if err != nil {
		return nil, err
	}

	server := &Server{
		logger:          logger,
		shutdownTimeout: DefaultShutdownTimeout,
		sqlDB:           sqlDB,
		authDeps:        authentication.Deps{Register: registration.Register, Login: login.Login},
		authorize:       authorization.Middleware(logger, tokens.ParseRequest, users),
		Config:          cfg,
	}
	server.setupRoutes()
	return server, nil
}
