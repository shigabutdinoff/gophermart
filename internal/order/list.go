package order

import (
	"context"
	"fmt"
)

// ListService отдаёт заказы пользователя в порядке, заданном ТЗ.
type ListService struct {
	orders Lister
}

func NewListService(orders Lister) *ListService {
	return &ListService{orders: orders}
}

func (s *ListService) List(ctx context.Context, userID int64) ([]Order, error) {
	orders, err := s.orders.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}

	return orders, nil
}
