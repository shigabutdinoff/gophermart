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
	// пауза общая для восстановления БД и подъёма очереди заданий
	DefaultRetryDelay = time.Second
)

// Option настраивает сервер до сборки его зависимостей.
type Option func(*serverOptions)

type serverOptions struct {
	shutdownTimeout time.Duration
}

// WithShutdownTimeout задаёт единый срок остановки сервера.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(options *serverOptions) { options.shutdownTimeout = timeout }
}

// Server запускает HTTP-сервер и останавливает его (graceful shutdown).
type Server struct {
	router           *chi.Mux
	logger           *zap.Logger
	runAddress       string
	requestBodyLimit int64
	shutdownTimeout  time.Duration
	retryDelay       time.Duration
	migrateDatabase  func(context.Context, *sql.DB) error
	ln               net.Listener
	srv              *http.Server
	sqlDB            *sql.DB
	deps             deps
}

// deps собирает всё, что сервер отдаёт маршрутам и фоновым задачам.
type deps struct {
	auth                authentication.Deps
	balance             balanceroute.Deps
	orders              ordersroute.Deps
	tokenParser         authorization.TokenParser
	pendingOrderResumer pendingOrderResumer
	// runner заполнен, только когда очередь способна выполнять задания
	runner *riverRunner
}

// pendingOrderResumer возвращает незавершённые заказы в очередь опроса.
type pendingOrderResumer interface {
	Resume(context.Context) (int, error)
}

// New создаёт сервер с переданной конфигурацией.
func New(logger *zap.Logger, cfg config.Config, options ...Option) (*Server, error) {
	secret, err := auth.EnsureSecret(logger, cfg.JWTSecret)
	if err != nil {
		return nil, err
	}
	cfg.JWTSecret = secret

	session, sqlDB := openDatabase(logger, cfg.DatabaseURI)
	server, err := newServer(logger, cfg, session, sqlDB, options...)
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
	options ...Option,
) (*Server, error) {
	secret, err := auth.ResolveSecret(cfg.JWTSecret)
	if err != nil {
		return nil, err
	}
	tokens, err := auth.NewJWTManager(secret)
	if err != nil {
		return nil, err
	}
	// ключ живёт в JWTManager, копию в конфигурации сервера не держим
	cfg.JWTSecret = ""

	settings := serverOptions{shutdownTimeout: DefaultShutdownTimeout}
	for _, apply := range options {
		apply(&settings)
	}

	built, err := buildDeps(depsOptions{
		logger:          logger,
		queue:           cfg.Queue,
		accrualAddress:  cfg.AccrualAddress,
		session:         session,
		sqlDB:           sqlDB,
		tokens:          tokens,
		shutdownTimeout: settings.shutdownTimeout,
	})
	if err != nil {
		return nil, err
	}

	server := &Server{
		logger:           logger,
		runAddress:       cfg.RunAddress,
		requestBodyLimit: cfg.RequestBodyLimit,
		shutdownTimeout:  settings.shutdownTimeout,
		retryDelay:       DefaultRetryDelay,
		migrateDatabase:  database.Migrate,
		sqlDB:            sqlDB,
		deps:             built,
	}
	server.setupRoutes()
	return server, nil
}

// depsOptions собирает всё, из чего строятся зависимости сервера.
// Пустой buildQueue означает боевую сборку очереди.
type depsOptions struct {
	logger          *zap.Logger
	queue           config.QueueConfig
	accrualAddress  string
	session         database.Session
	sqlDB           *sql.DB
	tokens          *auth.JWTManager
	shutdownTimeout time.Duration
	buildQueue      queueClientBuilder
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
		logger:          options.logger,
		queue:           options.queue,
		accrualAddress:  options.accrualAddress,
		storedOrders:    storedOrders,
		sqlDB:           options.sqlDB,
		shutdownTimeout: options.shutdownTimeout,
	})
	if err != nil {
		return deps{}, err
	}
	var pendingResumer pendingOrderResumer
	if queue.client != nil {
		dispatcher := jobs.NewDispatcher(queue.client, options.queue.Name)
		if err := storedOrders.AttachPusher(dispatcher); err != nil {
			return deps{}, fmt.Errorf("attach order job pusher: %w", err)
		}
		pendingResumer = jobs.NewPendingOrderResumer(storedOrders, dispatcher)
	}

	return deps{
		auth:                authDeps,
		balance:             buildBalanceDeps(storedBalance),
		orders:              buildOrderDeps(storedOrders),
		tokenParser:         options.tokens.ParseRequest,
		pendingOrderResumer: pendingResumer,
		runner:              queue.runner,
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
	dummyHash, err := auth.NewLoginDummyHash(passwords)
	if err != nil {
		return authentication.Deps{}, err
	}
	login, err := auth.NewLoginService(auth.LoginDeps{
		Logger: logger, Users: users, Verifier: passwords, Tokens: tokens,
		Limiter: auth.NewLoginLimiter(), DummyHash: dummyHash,
	})
	if err != nil {
		return authentication.Deps{}, err
	}

	return authentication.Deps{Register: registration.Register, Login: login.Login}, nil
}

// buildOrderDeps собирает загрузку и список заказов поверх их хранилища.
func buildOrderDeps(
	storedOrders *orderrepository.Repository,
) ordersroute.Deps {
	return ordersroute.Deps{
		Upload: order.NewUploadService(storedOrders).Upload,
		List:   order.NewListService(storedOrders).List,
	}
}
