package orders

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/order"
)

// Тексты ответов маршрутов заказов.
const (
	MessageEmptyNumber   = "Номер заказа не передан"
	MessageInvalidNumber = "Неверный формат номера заказа"
	MessageNumberTaken   = "Номер заказа уже загружен другим пользователем"
)

// bodyReadTimeout повторяет предел huma, он не достаётся телу без схемы
const bodyReadTimeout = 5 * time.Second

// Path маршрутов заказов, заданный ТЗ.
const ordersPath = "/api/user/orders"

// UploadFunc принимает номер заказа в том виде, в каком он пришёл в теле.
type UploadFunc func(ctx context.Context, number string, userID int64) error

// ListFunc отдаёт заказы пользователя от новых к старым.
type ListFunc func(ctx context.Context, userID int64) ([]order.Order, error)

type Deps struct {
	Upload UploadFunc
	List   ListFunc
}

// Options задаёт параметры публикации маршрутов заказов.
type Options struct {
	Middlewares huma.Middlewares
}

// uploadInput читает тело как есть, схема запроса заказу не нужна.
type uploadInput struct {
	RawBody []byte `contentType:"text/plain"`
}

// uploadOutput несёт только статус, тело успеха задано ТЗ пустым.
type uploadOutput struct {
	Status int
}

// orderView скрывает через omitempty только отсутствующее начисление.
type orderView struct {
	Number     string        `json:"number"`
	Status     order.Status  `json:"status"`
	Accrual    *money.Points `json:"accrual,omitempty"`
	UploadedAt time.Time     `json:"uploaded_at"`
}

// listOutput несёт готовое тело, huma сериализует поле Body даже для 204.
type listOutput struct {
	Status      int
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// RegisterRoutes публикует маршруты заказов как huma-операции.
func RegisterRoutes(
	api huma.API,
	logger *zap.Logger,
	deps Deps,
	options Options,
) {
	registerUploadRoute(api, logger, deps.Upload, options.Middlewares)
	registerListRoute(api, logger, deps.List, options.Middlewares)
}

func registerUploadRoute(
	api huma.API,
	logger *zap.Logger,
	upload UploadFunc,
	middlewares huma.Middlewares,
) {
	huma.Register(api, huma.Operation{
		OperationID: "upload-order",
		Method:      http.MethodPost,
		Path:        ordersPath,
		Summary:     "Загрузка номера заказа",
		// новый номер принят в обработку, повтор своего понижает статус
		DefaultStatus:   http.StatusAccepted,
		BodyReadTimeout: bodyReadTimeout,
		Middlewares:     middlewares,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusConflict,
			http.StatusUnprocessableEntity,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, in *uploadInput) (*uploadOutput, error) {
		userID, ok := authorization.UserID(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized(message.Unauthorized)
		}

		switch err := upload(ctx, string(in.RawBody), userID); {
		case err == nil:
			return &uploadOutput{Status: http.StatusAccepted}, nil
		case errors.Is(err, order.ErrAlreadyUploaded):
			return &uploadOutput{Status: http.StatusOK}, nil
		case errors.Is(err, order.ErrEmptyNumber):
			return nil, huma.Error400BadRequest(MessageEmptyNumber)
		case errors.Is(err, order.ErrInvalidNumber):
			return nil, huma.Error422UnprocessableEntity(MessageInvalidNumber)
		case errors.Is(err, order.ErrOwnedByAnother):
			return nil, huma.Error409Conflict(MessageNumberTaken)
		default:
			logger.Error("Не удалось принять номер заказа", zap.Error(err))
			return nil, huma.Error500InternalServerError(message.Internal)
		}
	})
}

func registerListRoute(
	api huma.API,
	logger *zap.Logger,
	list ListFunc,
	middlewares huma.Middlewares,
) {
	huma.Register(api, huma.Operation{
		OperationID:   "list-orders",
		Method:        http.MethodGet,
		Path:          ordersPath,
		Summary:       "Получение списка загруженных номеров заказов",
		DefaultStatus: http.StatusOK,
		Middlewares:   middlewares,
		Errors: []int{
			http.StatusUnauthorized,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, _ *struct{}) (*listOutput, error) {
		userID, ok := authorization.UserID(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized(message.Unauthorized)
		}

		orders, err := list(ctx, userID)
		if err != nil {
			logger.Error("Не удалось получить список заказов", zap.Error(err))
			return nil, huma.Error500InternalServerError(message.Internal)
		}
		if len(orders) == 0 {
			return &listOutput{Status: http.StatusNoContent}, nil
		}

		body, err := json.Marshal(orderViews(orders))
		if err != nil {
			logger.Error("Не удалось собрать список заказов", zap.Error(err))
			return nil, huma.Error500InternalServerError(message.Internal)
		}

		return &listOutput{
			Status:      http.StatusOK,
			ContentType: "application/json",
			Body:        body,
		}, nil
	})
}

func orderViews(orders []order.Order) []orderView {
	views := make([]orderView, len(orders))
	for i, stored := range orders {
		views[i] = orderView{
			Number:     stored.Number,
			Status:     stored.Status,
			Accrual:    stored.Accrual,
			UploadedAt: stored.UploadedAt,
		}
	}

	return views
}
