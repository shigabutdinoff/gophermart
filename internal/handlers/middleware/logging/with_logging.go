package logging

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// WithLogging логирует сведения о запросе без тел и заголовков.
func WithLogging(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				logger.Info(
					"Сведения о запросе",
					zap.String("uri", r.URL.Path),
					zap.String("method", r.Method),
					zap.Int("status", status),
					zap.Duration("duration", time.Since(start)),
					zap.Int("size", ww.BytesWritten()),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}
