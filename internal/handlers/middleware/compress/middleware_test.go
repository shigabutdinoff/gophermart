package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipBody(t *testing.T, s string) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return &buf
}

// rawClient даёт клиент без прозрачной декомпрессии и своего Accept-Encoding.
func rawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}

func do(t *testing.T, h http.Handler, accept string) *http.Response {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	return resp
}

func TestGzipMiddleware_CompressesResponseForGzipClient(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestGzipMiddleware_NoCompressionWithoutAcceptEncoding(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	resp := do(t, h, "")
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestGzipMiddleware_NoContentWithoutEncoding(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestGzipMiddleware_EmptyBodyWithoutEncoding(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, body)
}

func TestGzipMiddleware_EmptyWriteWithoutEncoding(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(nil)
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, body)
}

func TestGzipMiddleware_CompressesAfterEmptyWrite(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(nil)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

type headerSpy struct {
	http.ResponseWriter
	wrote bool
}

func (s *headerSpy) WriteHeader(code int) {
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}

func TestGzipMiddleware_PanicKeepsBufferedStatusUnsent(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("boom")
	}))

	spy := &headerSpy{ResponseWriter: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	require.PanicsWithValue(t, "boom", func() { h.ServeHTTP(spy, req) })

	assert.False(t, spy.wrote)
}

func TestGzipMiddleware_WriteHeaderAfterWriteIgnored(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		w.WriteHeader(http.StatusInternalServerError)
	}))

	for _, tc := range []struct {
		name   string
		accept string
	}{
		{"без сжатия", ""},
		{"с gzip", "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, h, tc.accept)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

func TestGzipMiddleware_FlushSendsCompressedData(t *testing.T) {
	release := make(chan struct{})
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event":"first"}`))
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("gzip-обёртка не реализует http.Flusher")
			return
		}
		f.Flush()
		<-release
		_, _ = w.Write([]byte(`{"event":"second"}`))
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()
	defer close(release)

	req, err := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))

	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	buf := make([]byte, len(`{"event":"first"}`))
	_, err = io.ReadFull(zr, buf)
	require.NoError(t, err)
	assert.Equal(t, `{"event":"first"}`, string(buf))
}

func TestGzipMiddleware_FlushBeforeBodyKeepsResponseValid(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("gzip-обёртка не реализует http.Flusher")
			return
		}
		f.Flush()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestGzipMiddleware_SetsVaryHeader(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	for _, accept := range []string{"gzip", ""} {
		resp := do(t, h, accept)
		assert.Equal(t, "Accept-Encoding", resp.Header.Get("Vary"))
		resp.Body.Close()
	}
}

func TestGzipMiddleware_PrecompressedResponseNotReencoded(t *testing.T) {
	pre := gzipBody(t, "precompressed payload")

	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(pre.Bytes())
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, "precompressed payload", string(body))
}
