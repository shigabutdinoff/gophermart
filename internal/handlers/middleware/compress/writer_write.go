package compress

func (c *compressWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.flushHeader(p)
	if c.sink.on {
		return c.sink.zw.Write(p)
	}
	return c.w.Write(p)
}
