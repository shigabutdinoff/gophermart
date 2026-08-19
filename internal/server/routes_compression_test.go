package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

// gzippedResponse читает ответ gzip-клиенту и распаковывает его тело
func gzippedResponse(t *testing.T, payload string) string {
	t.Helper()

	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/payload", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
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
	defer zr.Close()
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(body)
}

func TestRouter_CompressesSmallEligibleResponse(t *testing.T) {
	assert.JSONEq(t, `{"status":"ok"}`, gzippedResponse(t, `{"status":"ok"}`))
}

// chi ищет в Accept-Encoding подстроку gzip, поэтому q-значения
// и вторая строка заголовка на выбор кодировки не влияют.
// Тест фиксирует это упрощение, а не согласование по RFC 9110.
func TestRouter_ChiIgnoresAcceptEncodingQualityValues(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding []string
		wantEncoding   string
	}{
		{"нулевой вес не отключает сжатие", []string{"gzip;q=0"}, "gzip"},
		{"вторая строка заголовка не учитывается", []string{"br", "gzip"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, srv := newTestServer(t, zap.NewNop(), config.Default())
			s.router.Get("/payload", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			})

			req, err := http.NewRequest(http.MethodGet, srv.URL+"/payload", http.NoBody)
			require.NoError(t, err)
			for _, value := range tt.acceptEncoding {
				req.Header.Add("Accept-Encoding", value)
			}

			resp, err := rawClient().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantEncoding, resp.Header.Get("Content-Encoding"))
		})
	}
}

func TestRouter_NoCompressionWithoutAcceptEncoding(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	payload := `{"status":"ok","padding":"` +
		strings.Repeat("0123456789", 60) + `"}`
	s.router.Get("/payload", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	})

	req, err := http.NewRequest(
		http.MethodGet,
		srv.URL+"/payload",
		http.NoBody,
	)
	require.NoError(t, err)

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, payload, string(body))
}

func TestRouter_CompressesResponseForGzipClient(t *testing.T) {
	payload := `{"status":"ok","padding":"` + strings.Repeat("0123456789", 60) + `"}`

	assert.Contains(t, gzippedResponse(t, payload), `"status":"ok"`)
}

func TestRouter_DecompressesRequestBody(t *testing.T) {
	assertRouterDecompresses(t, " GZip ")
}

func TestRouter_DecompressesXGzipRequestBody(t *testing.T) {
	assertRouterDecompresses(t, "x-gzip")
}

// assertRouterDecompresses проверяет распаковку тела для одного имени кодировки
func assertRouterDecompresses(t *testing.T, encoding string) {
	t.Helper()

	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		assert.Empty(t, req.Header.Get("Content-Encoding"))
		assert.Equal(t, int64(-1), req.ContentLength)
		body, err := io.ReadAll(req.Body)
		if !assert.NoError(t, err) {
			return
		}
		_, _ = w.Write(body)
	})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", gzipBody(t, "12345678903"))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", encoding)

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
	called := false
	s.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		called = true
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
	assert.False(t, called)
}

func TestRouter_GzipCorruptionWhileReadingAnswers400(t *testing.T) {
	echo := newEchoServer(t)
	corrupted := bytes.Clone(gzipBody(t, "payload").Bytes())
	corrupted[len(corrupted)-1] ^= 0xff

	req, err := http.NewRequest(http.MethodPost, echo.http.URL+"/echo", bytes.NewReader(corrupted))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := echo.http.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.True(t, echo.called)
}

func TestRouter_RejectsUnsupportedRequestEncoding(t *testing.T) {
	tests := []struct {
		name      string
		encodings []string
	}{
		{"неизвестная кодировка", []string{"deflate"}},
		{"перечисление одинаковых", []string{"gzip, gzip"}},
		{"перечисление разных", []string{"deflate, gzip"}},
		{"два заголовка", []string{"gzip", "gzip"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, srv := newTestServer(t, zap.NewNop(), config.Default())
			called := false
			s.router.Post("/echo", func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", gzipBody(t, "payload"))
			require.NoError(t, err)
			for _, encoding := range tt.encodings {
				req.Header.Add("Content-Encoding", encoding)
			}

			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
			assert.False(t, called)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Empty(t, body)
		})
	}
}

func TestRouter_SkipsEncodingCheckForEmptyBody(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/user/register", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "deflate")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func gzipBody(t *testing.T, s string) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := io.WriteString(zw, s)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return &buf
}

// rawClient даёт клиент без прозрачной декомпрессии и своего Accept-Encoding.
func rawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}
