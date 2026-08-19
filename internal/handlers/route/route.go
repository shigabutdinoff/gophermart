package route

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

// jsonContentType сообщает тип тела, собранного в обход сериализатора huma.
const jsonContentType = "application/json"

// Options задаёт параметры публикации маршрутов.
type Options struct {
	BodyLimit   int64
	Middlewares huma.Middlewares
}

// MaxBody переводит общий предел тела в границу операции huma.
func (o Options) MaxBody() int64 {
	return apiconfig.MaxBodyBytes(o.BodyLimit)
}

// InternalError записывает причину в журнал и отдаёт клиенту общий текст.
func InternalError(logger *zap.Logger, reason string, err error) error {
	logger.Error(reason, zap.Error(err))

	return huma.Error500InternalServerError(message.Internal)
}

// JSONList несёт готовое тело, huma сериализует поле Body даже для 204.
type JSONList struct {
	Status      int
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// JSONOrNoContent отдаёт пустой список ответом 204, а непустой готовым телом.
func JSONOrNoContent[T any](items []T) (*JSONList, error) {
	if len(items) == 0 {
		return &JSONList{Status: http.StatusNoContent}, nil
	}

	body, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}

	return &JSONList{
		Status:      http.StatusOK,
		ContentType: jsonContentType,
		Body:        body,
	}, nil
}
