package apiconfig

import (
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
)

const (
	validationErrorsAsBadRequestKey = "gophermart.validation-errors-as-bad-request"
	humaValidationErrorMessage      = "validation failed"
)

var configureErrors = sync.OnceFunc(func() {
	next := huma.NewErrorWithContext
	huma.NewErrorWithContext = func(
		ctx huma.Context,
		status int,
		msg string,
		errs ...error,
	) huma.StatusError {
		if status == http.StatusUnprocessableEntity && msg == humaValidationErrorMessage && len(errs) > 0 && ctx != nil {
			if operation := ctx.Operation(); operation != nil {
				marked, _ := operation.Metadata[validationErrorsAsBadRequestKey].(bool)
				if marked {
					status = http.StatusBadRequest
				}
			}
		}

		return next(ctx, status, msg, errs...)
	}
})

// ValidationErrorsAsBadRequest помечает операции, для которых ошибки схемы
// входят в предусмотренный ТЗ ответ 400 вместо стандартного ответа huma 422.
func ValidationErrorsAsBadRequest() map[string]any {
	return map[string]any{validationErrorsAsBadRequestKey: true}
}

// New описывает API без служебных маршрутов фреймворка.
// Набор публичных путей задан ТЗ, схемы и документация в него не входят.
func New() huma.Config {
	configureErrors()

	config := huma.DefaultConfig("Gophermart", "1.0.0")
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	// хук добавляет ссылку на описание схемы в тело и заголовок Link
	config.CreateHooks = nil
	config.Transformers = nil

	return config
}

// FromHTTP переносит посредники маршрутизатора внутрь операции huma.
// Порядок сохраняется, первый посредник остаётся внешним.
func FromHTTP(middlewares ...func(http.Handler) http.Handler) huma.Middlewares {
	converted := make(huma.Middlewares, 0, len(middlewares))
	for _, middleware := range middlewares {
		converted = append(converted, fromHTTP(middleware))
	}

	return converted
}

func fromHTTP(middleware func(http.Handler) http.Handler) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		request, writer := humachi.Unwrap(ctx)
		passed := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			next(huma.WithContext(ctx, r.Context()))
		})
		middleware(passed).ServeHTTP(writer, request)
	}
}
