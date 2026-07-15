package compress

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressWriter_Unwrap(t *testing.T) {
	underlying := httptest.NewRecorder()
	cw := newCompressWriter(underlying)
	assert.Same(t, underlying, cw.Unwrap())
}

func TestCompressWriter_ReleaseKeepsClientUntouched(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)
	cw.Header().Set("Content-Type", "text/plain")
	_, err := cw.Write([]byte(strings.Repeat("a", sniffLen)))
	require.NoError(t, err)
	require.True(t, cw.sink.on)

	written := rec.Body.Len()
	cw.release()
	assert.Equal(t, written, rec.Body.Len())
	assert.Nil(t, cw.sink.zw)

	// Повторный вызов безопасен
	cw.release()
	assert.Equal(t, written, rec.Body.Len())
}

func TestCompressWriter_PrecompressedPassthroughWithoutBuffering(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)
	cw.Header().Set("Content-Encoding", "br")

	n, err := cw.Write([]byte("small"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	assert.False(t, cw.sink.on)
	assert.Equal(t, "small", rec.Body.String())
	assert.Equal(t, "br", rec.Header().Get("Content-Encoding"))
}
