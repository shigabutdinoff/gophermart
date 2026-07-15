package compress

import (
	"compress/gzip"
	"net/http"
)

// compressWriter сжимает тело ответа в gzip.
type compressWriter struct {
	w     http.ResponseWriter
	zw    *gzip.Writer
	wrote bool
}

func newCompressWriter(w http.ResponseWriter) *compressWriter {
	return &compressWriter{w: w, zw: gzip.NewWriter(w)}
}

func (c *compressWriter) Header() http.Header {
	return c.w.Header()
}

func (c *compressWriter) WriteHeader(statusCode int) {
	if c.wrote {
		return
	}
	c.wrote = true
	c.w.Header().Set("Content-Encoding", "gzip")
	c.w.WriteHeader(statusCode)
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.zw.Write(p)
}

func (c *compressWriter) Close() error {
	return c.zw.Close()
}
