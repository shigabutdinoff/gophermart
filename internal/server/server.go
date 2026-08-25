package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
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
	built, err := buildDeps(logger, gormDB, sqlDB, tokens)
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

// buildDeps собирает зависимости маршрутов поверх хранилищ и менеджера токенов.
func buildDeps(
	logger *zap.Logger,
	gormDB *gorm.DB,
	sqlDB *sql.DB,
	tokens *auth.JWTManager,
) (deps, error) {
	authDeps, err := buildAuthDeps(logger, gormDB, tokens)
	if err != nil {
		return deps{}, err
	}
	storedOrders := orderrepository.New(gormDB)
	client, err := newQueueClient(logger, sqlDB)
	if err != nil {
		return deps{}, err
	}
	if client != nil {
		dispatcher := jobs.NewDispatcher(client)
		if err := storedOrders.AttachPusher(dispatcher); err != nil {
			return deps{}, fmt.Errorf("attach order job pusher: %w", err)
		}
	}

	return deps{
		auth:        authDeps,
		orders:      buildOrderDeps(storedOrders),
		tokenParser: tokens.ParseRequest,
	}, nil
}

// newQueueClient поднимает очередь заданий поверх того же пула, что и gorm:
// иначе задание не попало бы в транзакцию загрузки заказа.
// Без базы очереди нет, но это не отказ: сервер поднимается и ждёт её.
func newQueueClient(logger *zap.Logger, sqlDB *sql.DB) (*river.Client[*sql.Tx], error) {
	if sqlDB == nil {
		return nil, nil
	}

	client, err := river.NewClient(riverdatabasesql.New(sqlDB), &river.Config{
		Logger: slog.New(zapslog.NewHandler(logger.Core())),
	})
	if err != nil {
		return nil, fmt.Errorf("start job queue: %w", err)
	}

	return client, nil
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
