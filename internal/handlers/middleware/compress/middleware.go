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
		// Заголовок может прийти в несколько строк, Get видит только первую
		accept := strings.Join(r.Header.Values("Accept-Encoding"), ",")
		if acceptsGzip(accept) {
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
