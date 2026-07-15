package compress

import "net/http"

func (c *compressWriter) flushHeader(body []byte) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	if c.status == 0 {
		c.status = http.StatusOK
	}
	encoded := c.w.Header().Get("Content-Encoding") != ""
	if len(body) > 0 && !encoded && compressibleStatus(c.status) {
		c.sink.enable(c.w)
		c.w.Header().Set("Content-Encoding", "gzip")
		c.w.Header().Del("Content-Length")
	}
	c.w.WriteHeader(c.status)
}

func (c *compressWriter) Flush() {
	if !c.wroteHeader {
		c.flushHeader(nil)
	}
	c.sink.flush()
	_ = http.NewResponseController(c.w).Flush()
}

func (c *compressWriter) Close() error {
	if c.status != 0 {
		c.flushHeader(nil)
	}
	return c.sink.close()
}
