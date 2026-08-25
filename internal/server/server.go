package server

import (
	"database/sql"
	"fmt"
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
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
	"github.com/shigabutdinoff/gophermart/internal/jobs"
	"github.com/shigabutdinoff/gophermart/internal/order"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
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
	deps            deps
	config.Config
}

// deps собирает всё, что сервер отдаёт маршрутам и фоновым задачам.
type deps struct {
	auth        authentication.Deps
	orders      ordersroute.Deps
	tokenParser authorization.TokenParser
	runner      *riverRunner
}

// New создаёт сервер с переданной конфигурацией.
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
	built, err := buildDeps(depsOptions{
		logger: logger,
		cfg:    cfg,
		now:    now,
		gormDB: gormDB,
		sqlDB:  sqlDB,
		tokens: tokens,
	})
	if err != nil {
		return nil, err
	}

	server := &Server{
		logger:          logger,
		shutdownTimeout: DefaultShutdownTimeout,
		sqlDB:           sqlDB,
		deps:            built,
		Config:          cfg,
	}
	server.setupRoutes()
	return server, nil
}

// depsOptions собирает всё, из чего строятся зависимости сервера.
// Пустой buildQueue означает боевую сборку очереди.
type depsOptions struct {
	logger      *zap.Logger
	cfg         config.Config
	now         auth.Clock
	gormDB      *gorm.DB
	sqlDB       *sql.DB
	tokens      *auth.JWTManager
	buildQueue  queueClientBuilder
	newThrottle queueThrottleFactory
}

// buildDeps собирает зависимости маршрутов поверх хранилищ и менеджера токенов.
func buildDeps(options depsOptions) (deps, error) {
	authDeps, err := buildAuthDeps(options.logger, options.gormDB, options.tokens)
	if err != nil {
		return deps{}, err
	}
	storedOrders := orderrepository.New(options.gormDB)
	buildQueue := options.buildQueue
	if buildQueue == nil {
		buildQueue = newQueueClient
	}
	queue, err := buildQueue(queueOptions{
		logger:       options.logger,
		cfg:          options.cfg,
		now:          options.now,
		storedOrders: storedOrders,
		sqlDB:        options.sqlDB,
		newThrottle:  options.newThrottle,
	})
	if err != nil {
		return deps{}, err
	}
	if queue.client != nil {
		dispatcher := jobs.NewDispatcher(queue.client, options.cfg.Queue.Name)
		if err := storedOrders.AttachPusher(dispatcher); err != nil {
			return deps{}, fmt.Errorf("attach order job pusher: %w", err)
		}
	}

	return deps{
		auth:        authDeps,
		orders:      buildOrderDeps(storedOrders),
		tokenParser: options.tokens.ParseRequest,
		runner:      queue.runner,
	}, nil
}

// buildAuthDeps собирает регистрацию и вход поверх хранилища пользователей.
func buildAuthDeps(
	logger *zap.Logger,
	gormDB *gorm.DB,
	tokens *auth.JWTManager,
) (authentication.Deps, error) {
	users := userrepository.New(gormDB)
	passwords := auth.Argon2Passwords{}
	registration := auth.NewRegisterService(users, passwords, tokens)
	login, err := auth.NewLoginService(logger, users, passwords, tokens)
	if err != nil {
		return authentication.Deps{}, err
	}

	return authentication.Deps{Register: registration.Register, Login: login.Login}, nil
}

func buildOrderDeps(storedOrders *orderrepository.Repository) ordersroute.Deps {
	return ordersroute.Deps{
		Upload: order.NewUploadService(storedOrders).Upload,
		List:   order.NewListService(storedOrders).List,
	}
}
