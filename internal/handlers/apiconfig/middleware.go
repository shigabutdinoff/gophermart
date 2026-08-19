package apiconfig

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5/middleware"
)

// AllowContentType переносит route-local фильтр типа содержимого внутрь
// Huma-операции.
func AllowContentType(contentTypes ...string) func(huma.Context, func(huma.Context)) {
	return fromRequestMiddleware(middleware.AllowContentType(contentTypes...))
}

var clientIPFromRemoteAddr = fromRequestMiddleware(middleware.ClientIPFromRemoteAddr)

// ClientIPFromRemoteAddr переносит адрес TCP-клиента в контекст Huma-операции.
func ClientIPFromRemoteAddr(ctx huma.Context, next func(huma.Context)) {
	clientIPFromRemoteAddr(ctx, next)
}

// fromRequestMiddleware поддерживает только изменение контекста запроса и
// досрочный ответ. Подмена http.ResponseWriter через этот адаптер не сохраняется.
func fromRequestMiddleware(httpMiddleware func(http.Handler) http.Handler) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		request, writer := humachi.Unwrap(ctx)
		passed := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			next(huma.WithContext(ctx, r.Context()))
		})
		httpMiddleware(passed).ServeHTTP(writer, request)
	}
}
