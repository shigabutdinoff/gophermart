package orders

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
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

type Deps struct {
	Upload UploadFunc
}

// uploadInput читает тело как есть, схема запроса заказу не нужна.
type uploadInput struct {
	RawBody []byte `contentType:"text/plain"`
}

// uploadOutput несёт только статус, тело успеха задано ТЗ пустым.
type uploadOutput struct {
	Status int
}

// RegisterRoutes публикует маршруты заказов как huma-операции.
func RegisterRoutes(
	api huma.API,
	logger *zap.Logger,
	deps Deps,
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

		switch err := deps.Upload(ctx, string(in.RawBody), userID); {
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
