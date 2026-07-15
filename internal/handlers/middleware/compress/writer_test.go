package compress

import (
	"net/http/httptest"
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
	_, err := cw.Write([]byte("partial body"))
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
