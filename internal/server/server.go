package server

import (
	"context"
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

	"github.com/shigabutdinoff/gophermart/internal/accrual"
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
	logger     *zap.Logger
	cfg        config.Config
	gormDB     *gorm.DB
	sqlDB      *sql.DB
	tokens     *auth.JWTManager
	buildQueue queueClientBuilder
}

type queueClientBuilder func(queueOptions) (queueParts, error)

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
		storedOrders: storedOrders,
		sqlDB:        options.sqlDB,
	})
	if err != nil {
		return deps{}, err
	}
	if queue.client != nil {
		dispatcher := jobs.NewDispatcher(queue.client)
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

// queueParts делит очередь на постановку заданий и их выполнение.
// Пустой runner означает очередь только для постановки заданий.
type queueParts struct {
	client *river.Client[*sql.Tx]
	runner *riverRunner
}

// queueOptions собирает очередь заданий вокруг хранилища заказов.
type queueOptions struct {
	logger       *zap.Logger
	cfg          config.Config
	storedOrders order.ResultWriter
	sqlDB        *sql.DB
}

// newQueueClient поднимает очередь заданий поверх того же пула, что и gorm:
// иначе задание не попало бы в транзакцию загрузки заказа.
// Без базы очереди нет, но это не отказ: сервер поднимается и ждёт её.
func newQueueClient(options queueOptions) (queueParts, error) {
	if options.sqlDB == nil {
		return queueParts{}, nil
	}

	queueConfig := &river.Config{Logger: queueLogger(options.logger)}
	workers := buildWorkers(options.logger, options.cfg, options.storedOrders)
	if workers != nil {
		queueConfig.Workers = workers
		queueConfig.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 4},
		}
		queueConfig.FetchPollInterval = jobs.DefaultPollInterval
	}

	client, err := river.NewClient(riverdatabasesql.New(options.sqlDB), queueConfig)
	if err != nil {
		return queueParts{}, fmt.Errorf("start job queue: %w", err)
	}
	if workers == nil {
		return queueParts{client: client}, nil
	}

	return queueParts{client: client, runner: newRiverRunner(client)}, nil
}

const pollModeNotice = "Driver does not support listener; entering poll only mode"

type noticeFreeHandler struct {
	slog.Handler
}

func (h noticeFreeHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level == slog.LevelInfo && record.Message == pollModeNotice {
		return nil
	}

	return h.Handler.Handle(ctx, record)
}

func (h noticeFreeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return noticeFreeHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h noticeFreeHandler) WithGroup(name string) slog.Handler {
	return noticeFreeHandler{Handler: h.Handler.WithGroup(name)}
}

func queueLogger(logger *zap.Logger) *slog.Logger {
	return slog.New(noticeFreeHandler{Handler: zapslog.NewHandler(logger.Core())})
}

// buildWorkers оставляет сервис без опроса расчёта, если его адрес непригоден.
func buildWorkers(
	logger *zap.Logger,
	cfg config.Config,
	storedOrders order.ResultWriter,
) *river.Workers {
	client, err := accrual.New(cfg.AccrualAddress)
	if err != nil {
		logger.Warn("Опрос системы расчёта отключён", zap.Error(err))

		return nil
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewCheckAccrual(logger, client, storedOrders, jobs.CheckAccrualOptions{}))

	return workers
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
