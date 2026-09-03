package server

import (
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
	s, srv := newTestServer(t, zap.New(core), config.Default())
	s.router.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

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
