package decompress

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/klauspost/compress/gzip"
)

// Encodings перечисляет имена Content-Encoding для распаковки.
var Encodings = []string{"gzip", "x-gzip"}

type gzipBody struct {
	*gzip.Reader
	source io.Closer
}

func (b *gzipBody) Close() error {
	return errors.Join(b.Reader.Close(), b.source.Close())
}

// Gzip распаковывает тело запроса, помеченное gzip.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encodings := r.Header.Values("Content-Encoding")
		if len(encodings) > 1 && r.ContentLength != 0 {
			// распаковать несколько кодировок сразу нечем
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		if len(encodings) == 0 || !isGzip(encodings[0]) {
			next.ServeHTTP(w, r)
			return
		}

		source := r.Body
		reader, err := gzip.NewReader(source)
		if err != nil {
			_ = source.Close()
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		r.Body = &gzipBody{Reader: reader, source: source}
		defer r.Body.Close()
		r.Header.Del("Content-Encoding")
		r.ContentLength = -1
		next.ServeHTTP(w, r)
	})
}

// isGzip сравнивает имя кодировки без учёта регистра и крайних пробелов.
func isGzip(encoding string) bool {
	name := strings.TrimSpace(encoding)
	return slices.ContainsFunc(Encodings, func(known string) bool {
		return strings.EqualFold(name, known)
	})
}
