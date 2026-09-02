package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func newTestServer(t *testing.T, logger *zap.Logger, cfg config.Config) (*Server, *httptest.Server) {
	t.Helper()

	s := mustNew(t, logger, cfg)
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)
	return s, srv
}

// readBody читает тело и отвечает статусом по ошибке, как обработчики
func readBody(w http.ResponseWriter, r *http.Request) bool {
	if _, err := io.ReadAll(r.Body); err != nil {
		status := http.StatusBadRequest
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, http.StatusText(status), status)
		return false
	}
	return true
}

// echoServer поднимает сервер с маршрутом /echo, читающим тело как обработчики
type echoServer struct {
	*Server
	http   *httptest.Server
	called bool
}

func newEchoServer(t *testing.T) *echoServer {
	t.Helper()

	server, httpServer := newTestServer(t, zap.NewNop(), config.Default())
	echo := &echoServer{Server: server, http: httpServer}
	server.router.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		echo.called = true
		if !readBody(w, req) {
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return echo
}

func TestRouter_PanicAnswersInternalError(t *testing.T) {
	s, srv := newTestServer(t, zap.NewNop(), config.Default())
	s.router.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	resp, err := srv.Client().Get(srv.URL + "/panic")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	resp2, err := srv.Client().Get(srv.URL + "/api/unknown")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestRouter_LogsEveryRequest(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	_, srv := newTestServer(t, zap.New(core), config.Default())

	reqs := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/ping", http.StatusServiceUnavailable},
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

	entries := observed.FilterField(
		zap.String("http.request.method", http.MethodGet),
	).All()
	entries = append(entries, observed.FilterField(
		zap.String("http.request.method", http.MethodPost),
	).All()...)
	require.Len(t, entries, len(reqs))

	byPath := make(map[string]map[string]any, len(entries))
	for _, entry := range entries {
		fields := entry.ContextMap()
		path, _ := fields["url.path"].(string)
		byPath[path+" "+fields["http.request.method"].(string)] = fields
	}
	for _, tc := range reqs {
		fields, ok := byPath[tc.path+" "+tc.method]
		require.True(t, ok, "нет записи для %s %s", tc.method, tc.path)
		assert.EqualValues(t, tc.want, fields["http.response.status_code"])
	}
}

// Заголовки безопасности стоят на всех ответах сервиса, включая отказы.
func TestServerRoutes_CarrySecurityHeaders(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())
	t.Cleanup(server.closeDatabase)

	for _, path := range []string{"/api/user/register", "/api/user/orders"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			response := httptest.NewRecorder()

			server.router.ServeHTTP(response, request)

			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}
