package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"gorm.io/gorm"
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
