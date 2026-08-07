package apiconfig

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5/middleware"
)

func AllowContentType(contentTypes ...string) func(huma.Context, func(huma.Context)) {
	return fromRequestMiddleware(middleware.AllowContentType(contentTypes...))
}

func fromRequestMiddleware(httpMiddleware func(http.Handler) http.Handler) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		request, writer := humachi.Unwrap(ctx)
		passed := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			next(huma.WithContext(ctx, r.Context()))
		})
		httpMiddleware(passed).ServeHTTP(writer, request)
	}
}
