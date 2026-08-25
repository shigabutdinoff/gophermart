package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/shigabutdinoff/gophermart/internal/accrual"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

// errOutsideTransaction ловит попытку поставить задание мимо транзакции заказа.
var errOutsideTransaction = errors.New("order transaction is not a sql transaction")

// CheckAccrualArgs несёт номер заказа, который предстоит опросить.
type CheckAccrualArgs struct {
	Number string `json:"number"`
}

// Kind называет задание в таблице очереди.
func (CheckAccrualArgs) Kind() string { return "check_accrual" }

// Inserter ставит задание в переданную транзакцию.
type Inserter interface {
	InsertTx(
		ctx context.Context,
		tx *sql.Tx,
		args river.JobArgs,
		opts *river.InsertOpts,
	) (*rivertype.JobInsertResult, error)
}

// Dispatcher ставит задание на опрос расчёта в транзакцию загрузки заказа.
type Dispatcher struct {
	queue Inserter
}

func NewDispatcher(queue Inserter) *Dispatcher {
	return &Dispatcher{queue: queue}
}

// Push кладёт задание той же транзакцией, что и вставку заказа: gorm держит
// её в ConnPool, очередь ждёт ту же *sql.Tx.
func (d *Dispatcher) Push(ctx context.Context, tx *gorm.DB, number string) error {
	sqlTx, ok := tx.Statement.ConnPool.(*sql.Tx)
	if !ok {
		return errOutsideTransaction
	}

	args := CheckAccrualArgs{Number: number}
	if _, err := d.queue.InsertTx(ctx, sqlTx, args, nil); err != nil {
		return fmt.Errorf("dispatch %s: %w", CheckAccrualArgs{}.Kind(), err)
	}

	return nil
}

// DefaultPollInterval отделяет опросы расчёта, пока он не довёл заказ.
const DefaultPollInterval = time.Second

// AccrualClient опрашивает внешнюю систему расчёта по номеру заказа.
type AccrualClient interface {
	OrderInfo(ctx context.Context, number string) (accrual.OrderInfo, error)
}

// CheckAccrualOptions задаёт паузу между опросами расчёта.
type CheckAccrualOptions struct {
	PollInterval time.Duration
}

// CheckAccrual доводит заказ до окончательного статуса, опрашивая расчёт.
type CheckAccrual struct {
	river.WorkerDefaults[CheckAccrualArgs]
	logger       *zap.Logger
	client       AccrualClient
	orders       order.ResultWriter
	pollInterval time.Duration
}

func NewCheckAccrual(
	logger *zap.Logger,
	client AccrualClient,
	orders order.ResultWriter,
	options CheckAccrualOptions,
) *CheckAccrual {
	if options.PollInterval <= 0 {
		options.PollInterval = DefaultPollInterval
	}

	return &CheckAccrual{
		logger:       logger,
		client:       client,
		orders:       orders,
		pollInterval: options.PollInterval,
	}
}

// Work опрашивает расчёт и переводит исход в судьбу заказа и задания.
// Пока расчёт не отказал, задание снузится: снуз не тратит попыток очереди,
// и заказ дожидается ответа столько, сколько расчёту потребуется.
func (c *CheckAccrual) Work(ctx context.Context, job *river.Job[CheckAccrualArgs]) error {
	number := job.Args.Number
	info, err := c.client.OrderInfo(ctx, number)
	if err != nil {
		return c.handleClientError(ctx, number, err)
	}

	return c.applyResult(ctx, number, info)
}

// giveUp закрывает заказ отказом расчёта, иначе он навсегда остался бы новым.
// Незаписанный отказ возвращает задание к опросу: отменённое некому повторить.
func (c *CheckAccrual) giveUp(ctx context.Context, number string, cause error) error {
	if _, err := c.orders.ApplyResult(ctx, number, order.StatusInvalid, nil); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.logger.Error(
			"Не удалось закрыть заказ отказом расчёта",
			zap.String("order", number),
			zap.Error(err),
		)
		if !errors.Is(err, order.ErrNotFound) {
			return river.JobSnooze(c.pollInterval)
		}
	}

	return river.JobCancel(cause)
}

// applyResult возвращает задание к опросу, пока расчёт не закончен.
// Строку заказа выбирает номер задания: ответу расчёта тут доверия нет.
func (c *CheckAccrual) applyResult(ctx context.Context, number string, info accrual.OrderInfo) error {
	finished, err := c.orders.ApplyResult(ctx, number, info.Status, info.Accrual)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.logger.Error(
			"Не удалось записать исход расчёта",
			zap.String("order", number),
			zap.Error(err),
		)
		// пропавший заказ не дождётся записи ни на одной попытке
		if errors.Is(err, order.ErrNotFound) {
			return river.JobCancel(err)
		}
		// недоступное хранилище заказу не приговор: опрос вернётся к нему позже
		return river.JobSnooze(c.pollInterval)
	}
	// завершённый сохранённый заказ опрашивать больше незачем
	if finished {
		return nil
	}

	return river.JobSnooze(c.pollInterval)
}

// handleClientError отделяет исходы, которые повтор уже не исправит.
func (c *CheckAccrual) handleClientError(ctx context.Context, number string, err error) error {
	// заказ ещё не зарегистрирован в системе расчёта
	if errors.Is(err, accrual.ErrNotRegistered) {
		return river.JobSnooze(c.pollInterval)
	}
	if isPermanent(err) {
		c.logger.Error(
			"Система расчёта отвечает непоправимо",
			zap.String("order", number),
			zap.Error(err),
		)

		return c.giveUp(ctx, number, err)
	}

	c.logger.Warn(
		"Опрос системы расчёта не удался",
		zap.String("order", number),
		zap.Error(err),
	)

	return river.JobSnooze(c.pollInterval)
}

// isPermanent помечает ответы, ради которых заказ опрашивать больше незачем.
func isPermanent(err error) bool {
	if errors.Is(err, accrual.ErrMalformedResponse) || errors.Is(err, accrual.ErrUnknownStatus) {
		return true
	}
	unexpected, ok := errors.AsType[*accrual.UnexpectedStatusError](err)

	return ok && unexpected.StatusCode < http.StatusInternalServerError
}
