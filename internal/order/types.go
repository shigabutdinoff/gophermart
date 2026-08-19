package order

import (
	"context"
	"errors"
	"time"
)

var (
	ErrEmptyNumber     = errors.New("order number is empty")
	ErrInvalidNumber   = errors.New("order number is invalid")
	ErrAlreadyUploaded = errors.New("order is already uploaded by the same user")
	ErrOwnedByAnother  = errors.New("order is uploaded by another user")
)

// Status повторяет статусы обработки расчёта из ТЗ.
type Status string

const (
	StatusNew        Status = "NEW"
	StatusProcessing Status = "PROCESSING"
	StatusInvalid    Status = "INVALID"
	StatusProcessed  Status = "PROCESSED"
)

type Order struct {
	Number     string
	UserID     int64
	Status     Status
	UploadedAt time.Time
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

// Lister отдаёт заказы пользователя от новых к старым.
type Lister interface {
	ListByUser(ctx context.Context, userID int64) ([]Order, error)
}
