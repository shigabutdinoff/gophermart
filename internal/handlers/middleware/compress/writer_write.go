package compress

import "net/http"

func (c *compressWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !c.needSniff() {
		return c.writeThrough(p)
	}
	return c.writeDetected(p)
}

func (c *compressWriter) writeThrough(p []byte) (int, error) {
	c.flushHeader(p)
	if c.sink.on {
		return c.sink.zw.Write(p)
	}
	return c.w.Write(p)
}

func (c *compressWriter) writeDetected(p []byte) (int, error) {
	ensureContentType(c.w.Header(), p)
	return c.writeThrough(p)
}

func (c *compressWriter) needSniff() bool {
	if c.wroteHeader {
		return false
	}
	// Уже закодированный хендлером ответ не сжимаем повторно
	if c.w.Header().Get("Content-Encoding") != "" {
		return false
	}
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	return compressibleStatus(status)
}
