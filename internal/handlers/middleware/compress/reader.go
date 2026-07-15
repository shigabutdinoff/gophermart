package compress

import (
	"compress/gzip"
	"errors"
	"io"
	"sync"
)

var gzipReaderPool = sync.Pool{
	New: func() any { return new(gzip.Reader) },
}

type compressReader struct {
	r  io.ReadCloser
	zr *gzip.Reader
}

func newCompressReader(r io.ReadCloser) (*compressReader, error) {
	zr := gzipReaderPool.Get().(*gzip.Reader)
	if err := zr.Reset(r); err != nil {
		gzipReaderPool.Put(zr)
		return nil, err
	}

	return &compressReader{
		r:  r,
		zr: zr,
	}, nil
}

func (c *compressReader) Read(p []byte) (n int, err error) {
	return c.zr.Read(p)
}

func (c *compressReader) Close() error {
	if c.zr == nil {
		return nil
	}
	err := errors.Join(c.r.Close(), c.zr.Close())
	gzipReaderPool.Put(c.zr)
	c.zr = nil
	return err
}
