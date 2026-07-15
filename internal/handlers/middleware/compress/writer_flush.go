package compress

import "net/http"

func (c *compressWriter) flushSniffed() error {
	body := c.sniff.take()
	_, err := c.writeDetected(body)
	return err
}

// flushPlain отправляет накопленное тело без сжатия, оно меньше порога.
func (c *compressWriter) flushPlain() error {
	body := c.sniff.take()
	ensureContentType(c.w.Header(), body)
	c.flushHeader(nil)
	_, err := c.w.Write(body)
	return err
}

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
	if c.sniff.buffered() > 0 {
		_ = c.flushSniffed()
	} else if !c.wroteHeader {
		c.flushHeader(nil)
	}
	c.sink.flush()
	_ = http.NewResponseController(c.w).Flush()
}

func (c *compressWriter) Close() error {
	if c.sniff.buffered() > 0 {
		if err := c.flushPlain(); err != nil {
			return err
		}
	} else if c.status != 0 {
		c.flushHeader(nil)
	}
	return c.sink.close()
}
