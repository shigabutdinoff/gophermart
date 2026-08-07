package server

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/balance"
	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/authentication"
	balanceroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/balance"
	ordersroute "github.com/shigabutdinoff/gophermart/internal/handlers/route/orders"
	"github.com/shigabutdinoff/gophermart/internal/jobs"
	"github.com/shigabutdinoff/gophermart/internal/order"
	balancerepository "github.com/shigabutdinoff/gophermart/internal/repository/balance"
	"github.com/shigabutdinoff/gophermart/internal/repository/database"
	orderrepository "github.com/shigabutdinoff/gophermart/internal/repository/order"
	userrepository "github.com/shigabutdinoff/gophermart/internal/repository/user"
)

const (
	DefaultShutdownTimeout   = 10 * time.Second
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
)

type Server struct {
	router           *chi.Mux
	logger           *zap.Logger
	runAddress       string
	requestBodyLimit int64
	shutdownTimeout  time.Duration
	ln               net.Listener
	srv              *http.Server
	sqlDB            *sql.DB
	deps             deps
}

// deps собирает всё, что сервер отдаёт маршрутам и фоновым задачам.
type deps struct {
	auth        authentication.Deps
	balance     balanceroute.Deps
	orders      ordersroute.Deps
	tokenParser authorization.TokenParser
	runner      *riverRunner
}

// New создаёт сервер с переданной конфигурацией.
func New(logger *zap.Logger, cfg config.Config) (*Server, error) {
	session, sqlDB := openDatabase(logger, cfg.DatabaseURI)
	server, err := newServer(logger, cfg, session, sqlDB)
	if err != nil {
		closeDatabaseHandle(logger, sqlDB)
	}

	return server, err
}

func newServer(
	logger *zap.Logger,
	cfg config.Config,
	session database.Session,
	sqlDB *sql.DB,
) (*Server, error) {
	secret, err := auth.ResolveSecret(cfg.JWTSecret)
	if err != nil {
		return nil, err
	}
	tokens, err := auth.NewJWTManager(secret)
	if err != nil {
		return nil, err
	}
	built, err := buildDeps(depsOptions{
		logger:         logger,
		queue:          cfg.Queue,
		accrualAddress: cfg.AccrualAddress,
		session:        session,
		sqlDB:          sqlDB,
		tokens:         tokens,
	})
	if err != nil {
		return nil, err
	}

	server := &Server{
		logger:           logger,
		runAddress:       cfg.RunAddress,
		requestBodyLimit: cfg.RequestBodyLimit,
		shutdownTimeout:  DefaultShutdownTimeout,
		sqlDB:            sqlDB,
		deps:             built,
	}
	server.setupRoutes()
	return server, nil
}

// depsOptions собирает всё, из чего строятся зависимости сервера.
// Пустой buildQueue означает боевую сборку очереди.
type depsOptions struct {
	logger         *zap.Logger
	queue          config.QueueConfig
	accrualAddress string
	session        database.Session
	sqlDB          *sql.DB
	tokens         *auth.JWTManager
	buildQueue     queueClientBuilder
}

// buildDeps собирает зависимости маршрутов поверх хранилищ и менеджера токенов.
func buildDeps(options depsOptions) (deps, error) {
	authDeps, err := buildAuthDeps(options.logger, options.session, options.tokens)
	if err != nil {
		return deps{}, err
	}
	storedOrders := orderrepository.New(options.session)
	storedBalance := balancerepository.New(options.session)
	buildQueue := options.buildQueue
	if buildQueue == nil {
		buildQueue = newQueueClient
	}
	queue, err := buildQueue(queueOptions{
		logger:         options.logger,
		queue:          options.queue,
		accrualAddress: options.accrualAddress,
		storedOrders:   storedOrders,
		sqlDB:          options.sqlDB,
	})
	if err != nil {
		return deps{}, err
	}
	if queue.client != nil {
		dispatcher := jobs.NewDispatcher(queue.client, options.queue.Name)
		if err := storedOrders.AttachPusher(dispatcher); err != nil {
			return deps{}, fmt.Errorf("attach order job pusher: %w", err)
		}
	}

	return deps{
		auth:        authDeps,
		balance:     buildBalanceDeps(storedBalance),
		orders:      buildOrderDeps(storedOrders),
		tokenParser: options.tokens.ParseRequest,
		runner:      queue.runner,
	}, nil
}

func buildBalanceDeps(storedBalance *balancerepository.Repository) balanceroute.Deps {
	return balanceroute.Deps{
		Read:     balance.NewReadService(storedBalance).Read,
		Withdraw: balance.NewWithdrawService(storedBalance).Withdraw,
		List:     balance.NewListService(storedBalance).List,
	}
}

// buildAuthDeps собирает регистрацию и вход поверх хранилища пользователей.
func buildAuthDeps(
	logger *zap.Logger,
	session database.Session,
	tokens *auth.JWTManager,
) (authentication.Deps, error) {
	users := userrepository.New(session)
	passwords := auth.Argon2Passwords{}
	registration := auth.NewRegisterService(users, passwords, tokens)
	login, err := auth.NewLoginService(logger, users, passwords, tokens)
	if err != nil {
		return authentication.Deps{}, err
	}

	return authentication.Deps{
		Register: registration.Register,
		Login: func(ctx context.Context, credentials auth.Credentials, client string) (auth.LoginResult, error) {
			return login.Login(ctx, credentials, client)
		},
	}, nil
}

func buildOrderDeps(storedOrders *orderrepository.Repository) ordersroute.Deps {
	return ordersroute.Deps{
		Upload: order.NewUploadService(storedOrders).Upload,
		List:   order.NewListService(storedOrders).List,
	}
}
