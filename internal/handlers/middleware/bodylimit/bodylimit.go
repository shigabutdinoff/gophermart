package bodylimit

import (
	"errors"
	"io"
	"net/http"
)

// Limit ограничивает размер тела запроса, превышение всплывает при чтении.
func Limit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Ранний отказ по Content-Length, у распакованного тела длина -1
			if r.ContentLength > limit {
				status := http.StatusRequestEntityTooLarge
				http.Error(w, http.StatusText(status), status)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ReadAll читает тело целиком, при ошибке сам отвечает подходящим статусом.
func ReadAll(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, http.StatusText(status), status)
		return nil, false
	}
	return body, true
}
