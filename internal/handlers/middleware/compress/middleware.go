package compress

import (
	"net/http"
	"strings"
)

// GzipMiddleware сжимает ответ и распаковывает тело запроса по заголовкам.
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

		if isGzipName(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
			cr, err := newCompressReader(r.Body)
			if err != nil {
				status := http.StatusBadRequest
				http.Error(w, http.StatusText(status), status)
				return
			}
			r.Body = cr
			defer cr.Close()
			r.Header.Del("Content-Encoding")
			r.ContentLength = -1
		}

		next.ServeHTTP(ow, r)

		// Не defer, при панике буфер должен остаться, чтобы recovery ответил 500
		if cw != nil {
			_ = cw.Close()
		}
	})
}
