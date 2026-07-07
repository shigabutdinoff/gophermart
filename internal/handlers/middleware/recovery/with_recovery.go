package recovery

import (
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// WithRecovery логирует панику и отвечает 500 либо обрывает начатый ответ.
func WithRecovery(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					logger.Error("Восстановление после паники",
						zap.Any("panic", rec),
						zap.ByteString("stack", debug.Stack()),
					)
					if ww.Status() == 0 {
						ww.WriteHeader(http.StatusInternalServerError)
						return
					}
					panic(http.ErrAbortHandler)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
