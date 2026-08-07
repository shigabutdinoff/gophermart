package decompress

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestGzip_DecompressesAndClosesRequestBody(t *testing.T) {
	assertDecompressedBody(t, " GZip ")
}

func TestGzip_DecompressesXGzipRequestBody(t *testing.T) {
	assertDecompressedBody(t, " X-GZip ")
}

// assertDecompressedBody проверяет распаковку тела для одного имени кодировки.
func assertDecompressedBody(t *testing.T, encoding string) {
	t.Helper()

	body := &trackingBody{Reader: gzipBytes(t, "payload")}
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Content-Encoding"))
		assert.Equal(t, int64(-1), r.ContentLength)
		got, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, "payload", string(got))
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Body = body
	request.ContentLength = 7
	request.Header.Set("Content-Encoding", encoding)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.True(t, body.closed)
}

func TestGzip_RejectsCorruptBodyAndClosesIt(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("not-gzip")}
	called := false
	handler := Gzip(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Body = body
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, response.Body.Bytes())
	assert.False(t, called)
	assert.True(t, body.closed)
}

func TestGzip_RejectsRepeatedContentEncodingHeader(t *testing.T) {
	compressed := gzipBytes(t, "payload").Bytes()
	body := &trackingBody{Reader: bytes.NewReader(compressed)}
	called := false
	handler := Gzip(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Body = body
	request.ContentLength = int64(len(compressed))
	request.Header.Add("Content-Encoding", "gzip")
	request.Header.Add("Content-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
	assert.Empty(t, response.Body.Bytes())
	assert.False(t, called)
}

func TestGzip_SkipsRepeatedHeaderCheckForEmptyBody(t *testing.T) {
	called := false
	handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.ContentLength = 0
	request.Header.Add("Content-Encoding", "br")
	request.Header.Add("Content-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.True(t, called)
}

func gzipBytes(t *testing.T, value string) *bytes.Buffer {
	t.Helper()

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := io.WriteString(writer, value)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return &compressed
}
