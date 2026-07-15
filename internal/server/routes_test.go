package server

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
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/bodylimit"
)

func newTestServer(t *testing.T, logger *zap.Logger, cfg config.Config) (*Server, *httptest.Server) {
	t.Helper()

	s := New(logger, cfg)
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)
	return s, srv
}

func TestRouter_PingRegistered(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	resp, err := srv.Client().Get(srv.URL + "/ping")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestRouter_PingBelowThresholdUncompressed(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ping", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestRouter_NoCompressionWithoutAcceptEncoding(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ping", http.NoBody)
	require.NoError(t, err)

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestRouter_PanicRecovered(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })

	resp, err := srv.Client().Get(srv.URL + "/panic")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	resp2, err := srv.Client().Get(srv.URL + "/api/unknown")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestRouter_PanicMidResponseAbortsConnection(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Тело больше порога сжатия уходит клиенту сразу, до паники
		_, _ = w.Write([]byte(strings.Repeat("partial ", 100)))
		panic("boom")
	})

	resp, err := srv.Client().Get(srv.URL + "/broken")
	if err == nil {
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
	}
	assert.Error(t, err)
}

func TestRouter_PanicWhileSniffingAnswers500(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/broken", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	})

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/broken", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestRouter_PanicAfterBufferedStatusAnswers500(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/panic", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("boom")
	})

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/panic", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestRouter_LogsEveryRequest(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	_, srv := newTestServer(t, zap.New(core), config.Default())

	reqs := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/ping", http.StatusOK},
		{http.MethodPost, "/ping", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/unknown", http.StatusNotFound},
	}
	for _, tc := range reqs {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, http.NoBody)
		require.NoError(t, err)
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, tc.want, resp.StatusCode)
	}

	entries := observed.FilterMessage("Сведения о запросе").All()
	require.Len(t, entries, len(reqs))
	for i, tc := range reqs {
		fields := entries[i].ContextMap()
		assert.Equal(t, tc.method, fields["method"])
		assert.Equal(t, tc.path, fields["uri"])
		assert.EqualValues(t, tc.want, fields["status"])
	}
}

func TestRouter_CompressesResponseForGzipClient(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/payload", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","padding":"` + strings.Repeat("0123456789", 60) + `"}`))
	})

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/payload", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"status":"ok"`)
}

func TestRouter_DecompressesRequestBody(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
	})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", gzipBody(t, "12345678903"))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "12345678903", string(body))
}

func TestRouter_BadGzipBodyRejected(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		w.WriteHeader(http.StatusOK)
	})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", strings.NewReader("not-gzip"))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouter_RejectsGzipBombByUncompressedSize(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := bodylimit.ReadAll(w, req); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, err := zw.Write(make([]byte, s.RequestBodyLimit+1))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", &bomb)
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestRouter_AcceptsBodyWithCompressedLengthAboveLimit(t *testing.T) {
	cfg := config.Default()
	cfg.RequestBodyLimit = 64
	s, srv := newTestServer(t, zap.NewNop(), cfg)
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := bodylimit.ReadAll(w, req); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Случайные байты не сжимаются, gzip делает их только длиннее
	rnd := rand.New(rand.NewPCG(1, 2))
	raw := make([]byte, s.RequestBodyLimit-4)
	for i := range raw {
		raw[i] = byte(rnd.UintN(256))
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.Greater(t, int64(buf.Len()), s.RequestBodyLimit)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRouter_RejectsHugeBodyByContentLength(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	called := false
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		called = true
		if _, ok := bodylimit.ReadAll(w, req); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	body := bytes.NewReader(make([]byte, s.RequestBodyLimit+1))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", body)
	require.NoError(t, err)

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.False(t, called)
}

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
