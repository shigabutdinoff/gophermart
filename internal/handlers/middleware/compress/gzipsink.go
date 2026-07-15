package compress

import (
	"compress/gzip"
	"io"
	"sync"
)

var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(io.Discard) },
}

// gzipSink держит gzip.Writer из пула на время одного ответа.
type gzipSink struct {
	zw *gzip.Writer
	on bool
}

func (g *gzipSink) enable(w io.Writer) {
	g.zw = gzipWriterPool.Get().(*gzip.Writer)
	g.zw.Reset(w)
	g.on = true
}

func (g *gzipSink) close() error {
	if !g.on {
		return nil
	}
	err := g.zw.Close()
	gzipWriterPool.Put(g.zw)
	g.zw, g.on = nil, false
	return err
}

// release отбрасывает недосланный буфер и возвращает писатель в пул.
func (g *gzipSink) release() {
	if g.zw == nil {
		return
	}
	g.zw.Reset(io.Discard)
	_ = g.zw.Close()
	gzipWriterPool.Put(g.zw)
	g.zw, g.on = nil, false
}
