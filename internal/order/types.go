package order

import (
	"context"
	"errors"
	"time"

	"github.com/shigabutdinoff/gophermart/internal/money"
)

var (
	ErrAlreadyUploaded = errors.New("order is already uploaded by the same user")
	ErrOwnedByAnother  = errors.New("order is uploaded by another user")
	ErrNotFound        = errors.New("order is not found")
)

// Status повторяет статусы обработки расчёта из ТЗ.
type Status string

const (
	StatusNew        Status = "NEW"
	StatusProcessing Status = "PROCESSING"
	StatusInvalid    Status = "INVALID"
	StatusProcessed  Status = "PROCESSED"
)

// FinalStatuses перечисляет статусы, после которых заказ не переписывается.
func FinalStatuses() []Status {
	return []Status{StatusProcessed, StatusInvalid}
}

// IsFinal отвечает, довёл ли расчёт заказ до конца.
func (s Status) IsFinal() bool {
	return s == StatusProcessed || s == StatusInvalid
}

type Order struct {
	Number     string
	UserID     int64
	Status     Status
	UploadedAt time.Time
	// nil отличает отсутствие начисления от начисленного нуля
	Accrual *money.Points
}

type CreateOutcome uint8

const (
	_ CreateOutcome = iota
	Created
	AlreadyOwned
	OwnedByAnother
)

// Creator сохраняет номер и классифицирует результат создания.
type Creator interface {
	CreateOrFindOwner(
		ctx context.Context,
		number string,
		userID int64,
	) (CreateOutcome, error)
}

// ResultWriter пишет исход расчёта и отвечает, закончен ли заказ.
type ResultWriter interface {
	ApplyResult(
		ctx context.Context,
		number string,
		status Status,
		accrual *money.Points,
	) (bool, error)
}

// Lister отдаёт заказы пользователя от новых к старым.
type Lister interface {
	ListByUser(ctx context.Context, userID int64) ([]Order, error)
}
