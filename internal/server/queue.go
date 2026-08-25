package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/shigabutdinoff/gophermart/internal/accrual"
	"github.com/shigabutdinoff/gophermart/internal/auth"
	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/jobs"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

type queueClientBuilder func(queueOptions) (queueParts, error)

// queueParts делит очередь на постановку заданий и их выполнение.
// Пустой runner означает очередь только для постановки заданий.
type queueParts struct {
	client *river.Client[*sql.Tx]
	runner *riverRunner
}

type queueThrottle interface {
	jobs.Throttler
	throttleLifecycle
	Attach(jobs.QueueController) error
}

type queueThrottleFactory func(*zap.Logger, string, auth.Clock) queueThrottle

// queueOptions собирает очередь заданий вокруг хранилища заказов.
type queueOptions struct {
	logger       *zap.Logger
	cfg          config.Config
	now          auth.Clock
	storedOrders order.ResultWriter
	sqlDB        *sql.DB
	newThrottle  queueThrottleFactory
}

// newQueueClient поднимает очередь заданий поверх того же пула, что и gorm:
// иначе задание не попало бы в транзакцию загрузки заказа.
// Без базы очереди нет, но это не отказ: сервер поднимается и ждёт её.
func newQueueClient(options queueOptions) (queueParts, error) {
	if options.sqlDB == nil {
		return queueParts{}, nil
	}
	newThrottle := options.newThrottle
	if newThrottle == nil {
		newThrottle = func(logger *zap.Logger, name string, now auth.Clock) queueThrottle {
			return jobs.NewThrottle(logger, name, now)
		}
	}

	cfg := options.cfg
	queueConfig := newQueueConfig(options.logger, cfg)
	accrualClient, accrualErr := accrual.New(cfg.AccrualAddress)
	var throttle queueThrottle
	if accrualErr != nil {
		options.logger.Warn("Опрос системы расчёта отключён", zap.Error(accrualErr))
	} else {
		throttle = newThrottle(options.logger, cfg.Queue.Name, options.now)
		queueConfig.Workers = buildWorkers(
			options.logger,
			cfg,
			options.storedOrders,
			accrualClient,
			throttle,
		)
		queueConfig.Queues = map[string]river.QueueConfig{
			cfg.Queue.Name: {MaxWorkers: cfg.Queue.Workers},
		}
	}

	client, err := river.NewClient(riverdatabasesql.New(options.sqlDB), queueConfig)
	if err != nil {
		return queueParts{}, fmt.Errorf("start job queue: %w", err)
	}
	if throttle == nil {
		return queueParts{client: client}, nil
	}
	if err := throttle.Attach(client); err != nil {
		return queueParts{}, fmt.Errorf("attach queue throttle: %w", err)
	}

	return queueParts{
		client: client,
		runner: newRiverRunner(client, throttle),
	}, nil
}

func newQueueConfig(
	logger *zap.Logger,
	cfg config.Config,
) *river.Config {
	return &river.Config{
		Logger:            queueLogger(logger),
		MaxAttempts:       cfg.Queue.MaxAttempts,
		FetchPollInterval: cfg.Queue.FetchPollInterval,
	}
}

// pollModeNotice называет пояснение очереди, которое нам говорить не о чем:
// режим опроса вместо LISTEN/NOTIFY выбран нами сознательно.
const pollModeNotice = "Driver does not support listener; entering poll only mode"

// noticeFreeHandler убирает одно пояснение, оставляя очереди остальной info:
// исходы заданий она пишет тем же уровнем, и глушить его целиком нельзя.
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
	client jobs.AccrualClient,
	throttle jobs.Throttler,
) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewCheckAccrual(logger, client, storedOrders, jobs.CheckAccrualOptions{
		PollInterval:    cfg.Queue.AccrualPollInterval,
		ThrottleBackoff: cfg.Queue.ThrottleBackoff,
		Throttle:        throttle,
	}))

	return workers
}
