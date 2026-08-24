package accrual

import (
	"fmt"

	"github.com/shigabutdinoff/gophermart/internal/order"
)

// Status повторяет статусы расчёта внешней системы.
type Status string

const (
	StatusRegistered Status = "REGISTERED"
	StatusProcessing Status = "PROCESSING"
	StatusInvalid    Status = "INVALID"
	StatusProcessed  Status = "PROCESSED"
)

// ToOrderStatus переводит статус расчёта в статус заказа.
func (s Status) ToOrderStatus() (order.Status, error) {
	switch s {
	case StatusRegistered, StatusProcessing:
		return order.StatusProcessing, nil
	case StatusInvalid:
		return order.StatusInvalid, nil
	case StatusProcessed:
		return order.StatusProcessed, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownStatus, s)
	}
}
