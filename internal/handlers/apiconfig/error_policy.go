package apiconfig

import (
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

const (
	validationErrorsAsBadRequestKey = "gophermart.validation-errors-as-bad-request"
	humaValidationErrorMessage      = "validation failed"
)

type errorFactory = func(huma.Context, int, string, ...error) huma.StatusError

func newErrorPolicyInstaller(target *errorFactory) func() {
	return sync.OnceFunc(func() {
		next := *target
		*target = func(
			ctx huma.Context,
			status int,
			msg string,
			errs ...error,
		) huma.StatusError {
			if validationErrorsAreBadRequest(ctx, status, msg, errs) {
				status = http.StatusBadRequest
			}

			return next(ctx, status, msg, errs...)
		}
	})
}

func validationErrorsAreBadRequest(ctx huma.Context, status int, msg string, errs []error) bool {
	if ctx == nil ||
		status != http.StatusUnprocessableEntity ||
		msg != humaValidationErrorMessage ||
		len(errs) == 0 {
		return false
	}

	operation := ctx.Operation()
	if operation == nil {
		return false
	}
	marked, _ := operation.Metadata[validationErrorsAsBadRequestKey].(bool)

	return marked
}

var installErrorPolicyOnce = newErrorPolicyInstaller(&huma.NewErrorWithContext)

// InstallErrorPolicy устанавливает глобальную политику Huma: ошибки валидации
// со статусом 422 получают статус 400 только у операций, помеченных
// ValidationErrorsAsBadRequest. Её нужно вызвать при запуске до начала обработки
// запросов. Параллельные вызовы самой функции безопасны, но безопасное изменение
// глобальной настройки Huma одновременно с обработкой запросов не гарантируется.
func InstallErrorPolicy() {
	installErrorPolicyOnce()
}

// ValidationErrorsAsBadRequest помечает операции, для которых ошибки схемы
// входят в предусмотренный ТЗ ответ 400 вместо стандартного ответа huma 422.
func ValidationErrorsAsBadRequest() map[string]any {
	return map[string]any{validationErrorsAsBadRequestKey: true}
}

// NewAPI собирает API сервиса вместе с политикой ошибок.
// Без неё ошибки схемы ушли бы клиенту статусом 422 вместо 400.
func NewAPI(router chi.Router) huma.API {
	InstallErrorPolicy()

	return humachi.New(router, New())
}
