package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGzipSink_EnableCompressesThenClose(t *testing.T) {
	var buf bytes.Buffer
	var g gzipSink

	g.enable(&buf)
	require.True(t, g.on)

	_, err := g.zw.Write([]byte("hello gzip"))
	require.NoError(t, err)
	require.NoError(t, g.close())
	assert.False(t, g.on)
	assert.Nil(t, g.zw)

	zr, err := gzip.NewReader(&buf)
	require.NoError(t, err)
	got, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, "hello gzip", string(got))
}

func TestGzipSink_CloseWhenDisabled(t *testing.T) {
	var g gzipSink
	assert.NoError(t, g.close())
}

func TestGzipSink_ReleaseIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	var g gzipSink
	g.enable(&buf)

	g.release()
	assert.False(t, g.on)
	assert.Nil(t, g.zw)

	// Повторный вызов безопасен
	g.release()
	assert.Nil(t, g.zw)
}
