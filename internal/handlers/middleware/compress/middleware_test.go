package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
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

func bigJSON() string {
	return `{"status":"ok","padding":"` + strings.Repeat("a", sniffLen) + `"}`
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

func TestGzipMiddleware_DecompressesRequest(t *testing.T) {
	var got string
	h := GzipMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		got = string(b)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", gzipBody(t, "12345678903"))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "12345678903", got)
}

func TestGzipMiddleware_DecompressesXGzipRequest(t *testing.T) {
	var got string
	h := GzipMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		got = string(b)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", gzipBody(t, "12345678903"))
	req.Header.Set("Content-Encoding", "x-gzip")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "12345678903", got)
}

func TestGzipMiddleware_CleansRequestMetadata(t *testing.T) {
	var encoding string
	var length int64
	h := GzipMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		encoding = r.Header.Get("Content-Encoding")
		length = r.ContentLength
	}))

	req := httptest.NewRequest(http.MethodPost, "/", gzipBody(t, "12345678903"))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Empty(t, encoding)
	assert.Equal(t, int64(-1), length)
}

func TestGzipMiddleware_RejectsInvalidGzip(t *testing.T) {
	called := false
	h := GzipMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("definitely-not-gzip"))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, http.StatusText(http.StatusBadRequest)+"\n", rec.Body.String())
	assert.False(t, called)
}

func TestGzipMiddleware_PassthroughWithoutHeader(t *testing.T) {
	var got string
	h := GzipMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		got = string(b)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("plain-body"))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "plain-body", got)
}

func TestGzipMiddleware_CompressesResponseForGzipClient(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bigJSON()))
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.JSONEq(t, bigJSON(), string(body))
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
		_, _ = w.Write([]byte(bigJSON()))
	}))

	resp := do(t, h, "gzip")
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.JSONEq(t, bigJSON(), string(body))
}

func TestGzipMiddleware_SniffsAcrossChunks(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<h"))
		_, _ = w.Write([]byte("tml><body>" + strings.Repeat("привет ", 100) + "</body></html>"))
	}))

	plainResp := do(t, h, "")
	defer plainResp.Body.Close()
	gzipResp := do(t, h, "gzip")
	defer gzipResp.Body.Close()

	want := plainResp.Header.Get("Content-Type")
	assert.Contains(t, want, "text/html")
	assert.Equal(t, want, gzipResp.Header.Get("Content-Type"))

	zr, err := gzip.NewReader(gzipResp.Body)
	require.NoError(t, err)
	gzBody, err := io.ReadAll(zr)
	require.NoError(t, err)
	plainBody, err := io.ReadAll(plainResp.Body)
	require.NoError(t, err)
	assert.Equal(t, plainBody, gzBody)
}

func TestGzipMiddleware_SniffsContentType(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	plainResp := do(t, h, "")
	defer plainResp.Body.Close()
	gzipResp := do(t, h, "gzip")
	defer gzipResp.Body.Close()

	plain := plainResp.Header.Get("Content-Type")
	assert.NotEmpty(t, plain)
	assert.Equal(t, plain, gzipResp.Header.Get("Content-Type"))
}

func TestGzipMiddleware_CompressionThreshold(t *testing.T) {
	cases := []struct {
		name string
		size int
		want bool
	}{
		{"меньше порога", sniffLen - 1, false},
		{"равно порогу", sniffLen, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte(strings.Repeat("a", tc.size)))
			}))

			resp := do(t, h, "gzip")
			defer resp.Body.Close()

			gzipped := resp.Header.Get("Content-Encoding") == "gzip"
			assert.Equal(t, tc.want, gzipped)

			var body []byte
			var err error
			if gzipped {
				zr, zerr := gzip.NewReader(resp.Body)
				require.NoError(t, zerr)
				body, err = io.ReadAll(zr)
			} else {
				body, err = io.ReadAll(resp.Body)
			}
			require.NoError(t, err)
			assert.Len(t, body, tc.size)
		})
	}
}

func TestGzipMiddleware_ErrorResponseNotCompressed(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusBadRequest,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(strings.Repeat("a", sniffLen)))
			}))

			resp := do(t, h, "gzip")
			defer resp.Body.Close()

			require.Equal(t, status, resp.StatusCode)
			assert.Empty(t, resp.Header.Get("Content-Encoding"))
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Len(t, body, sniffLen)
		})
	}
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

func TestGzipMiddleware_ParsesAcceptEncoding(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bigJSON()))
	}))

	cases := []struct {
		accept string
		want   bool
	}{
		{"gzip;q=0", false},
		{"gzip; q=0", false},
		{"gzip;q=0.0", false},
		{"deflate, gzip;q=0", false},
		{"gzip", true},
		{"gzip;q=1.0", true},
		{"deflate, gzip;q=0.5", true},
		{"GZIP", true},
		{"x-gzip", true},
		{"*", true},
		{"*;q=0", false},
		{"deflate, *", true},
		{"*, gzip;q=0", false},
		{"deflate", false},
	}
	for _, tc := range cases {
		t.Run(tc.accept, func(t *testing.T) {
			resp := do(t, h, tc.accept)
			defer resp.Body.Close()

			gzipped := resp.Header.Get("Content-Encoding") == "gzip"
			assert.Equal(t, tc.want, gzipped)
		})
	}
}

func TestGzipMiddleware_AcceptEncodingAcrossHeaderLines(t *testing.T) {
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bigJSON()))
	}))

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Add("Accept-Encoding", "deflate")
	req.Header.Add("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
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
	// Случайные байты не сжимаются, сжатое тело заведомо больше порога
	rnd := rand.New(rand.NewPCG(3, 4))
	plain := make([]byte, sniffLen+64)
	for i := range plain {
		plain[i] = byte(rnd.UintN(256))
	}
	pre := gzipBody(t, string(plain))
	require.GreaterOrEqual(t, pre.Len(), sniffLen)

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
	assert.Equal(t, plain, body)
}
