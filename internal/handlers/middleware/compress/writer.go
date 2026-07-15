package compress

import "net/http"

// compressWriter откладывает заголовок и по началу тела решает, сжимать ли.
type compressWriter struct {
	w           http.ResponseWriter
	sniff       sniffer
	sink        gzipSink
	status      int
	wroteHeader bool
}

func newCompressWriter(w http.ResponseWriter) *compressWriter {
	return &compressWriter{w: w}
}

func (c *compressWriter) Header() http.Header {
	return c.w.Header()
}

// Unwrap открывает http.ResponseController путь к нижележащему writer.
func (c *compressWriter) Unwrap() http.ResponseWriter {
	return c.w
}

func (c *compressWriter) WriteHeader(statusCode int) {
	if c.wroteHeader || c.status != 0 || c.sniff.buffered() > 0 {
		return
	}
	if statusCode < 200 {
		c.w.WriteHeader(statusCode)
		return
	}
	c.status = statusCode
}

func (c *compressWriter) release() {
	c.sink.release()
}
