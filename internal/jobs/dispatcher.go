package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// CheckAccrualArgs несёт номер заказа, который предстоит опросить.
type CheckAccrualArgs struct {
	Number string `json:"number"`
}

// Kind называет задание в таблице очереди.
func (CheckAccrualArgs) Kind() string { return "check_accrual" }

// Inserter ставит задание сразу или в переданную транзакцию.
type Inserter interface {
	Insert(
		ctx context.Context,
		args river.JobArgs,
		opts *river.InsertOpts,
	) (*rivertype.JobInsertResult, error)

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
	name  string
}

// NewDispatcher ставит задания в названную очередь, пустое имя отдаёт выбор ей.
func NewDispatcher(queue Inserter, name string) *Dispatcher {
	return &Dispatcher{queue: queue, name: name}
}

func (d *Dispatcher) insertOptions() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:      d.name,
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}

// Push кладёт задание той же транзакцией, что и вставку заказа.
func (d *Dispatcher) Push(ctx context.Context, tx *sql.Tx, number string) error {
	args := CheckAccrualArgs{Number: number}
	if _, err := d.queue.InsertTx(ctx, tx, args, d.insertOptions()); err != nil {
		return fmt.Errorf("dispatch %s: %w", CheckAccrualArgs{}.Kind(), err)
	}

	return nil
}

// Resume возвращает незавершённые заказы к опросу без повторной постановки
// одинаковых заданий.
func (d *Dispatcher) Resume(ctx context.Context, numbers []string) (int, error) {
	inserted := 0
	for _, number := range numbers {
		result, err := d.queue.Insert(ctx, CheckAccrualArgs{Number: number}, d.insertOptions())
		if err != nil {
			return inserted, fmt.Errorf("dispatch %s: %w", CheckAccrualArgs{}.Kind(), err)
		}
		if !result.UniqueSkippedAsDuplicate {
			inserted++
		}
	}

	return inserted, nil
}
