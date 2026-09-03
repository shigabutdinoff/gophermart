package order

import (
	"context"
	"fmt"

	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

// UploadService принимает номер заказа и определяет исход загрузки.
type UploadService struct {
	orders Creator
}

func NewUploadService(orders Creator) *UploadService {
	return &UploadService{orders: orders}
}

// Upload принимает сырой номер, нормализует и валидирует его.
func (s *UploadService) Upload(ctx context.Context, number string, userID int64) error {
	number, err := ordernumber.Parse(number)
	if err != nil {
		return err
	}

	outcome, err := s.orders.CreateOrFindOwner(ctx, number, userID)
	if err != nil {
		return fmt.Errorf("create order: %w", err)
	}

	switch outcome {
	case Created:
		return nil
	case AlreadyOwned:
		return ErrAlreadyUploaded
	case OwnedByAnother:
		return ErrOwnedByAnother
	default:
		return fmt.Errorf("create order: unknown outcome %d", outcome)
	}
}
