package jobs

import (
	"context"

	"github.com/shigabutdinoff/gophermart/internal/order"
)

// PendingOrderResumer возвращает незавершённые заказы в очередь опроса.
type PendingOrderResumer struct {
	orders     order.PendingLister
	dispatcher *Dispatcher
}

func NewPendingOrderResumer(orders order.PendingLister, dispatcher *Dispatcher) *PendingOrderResumer {
	return &PendingOrderResumer{orders: orders, dispatcher: dispatcher}
}

func (r *PendingOrderResumer) Resume(ctx context.Context) (int, error) {
	numbers, err := r.orders.ListUnfinished(ctx)
	if err != nil {
		return 0, err
	}

	return r.dispatcher.Resume(ctx, numbers)
}
