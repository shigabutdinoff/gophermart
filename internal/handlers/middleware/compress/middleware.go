package compress

import (
	"net/http"
	"strings"
)

// GzipMiddleware сжимает ответ по заголовку Accept-Encoding.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")

		ow := w

		var cw *compressWriter
		if strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") {
			cw = newCompressWriter(w)
			ow = cw
			// При панике писатель возвращается в пул без отправки буфера
			defer cw.release()
		}

		next.ServeHTTP(ow, r)

		// Не defer, при панике буфер должен остаться, чтобы recovery ответил 500
		if cw != nil {
			_ = cw.Close()
		}
	})
}
