package server

import (
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

	s := New(logger, cfg)
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)
	return s, srv
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
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	})

	resp, err := srv.Client().Get(srv.URL + "/broken")
	if err == nil {
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
	}
	assert.Error(t, err)
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

	entries := observed.FilterMessage("Сведения о запросе").All()
	require.Len(t, entries, len(reqs))
	for i, tc := range reqs {
		fields := entries[i].ContextMap()
		assert.Equal(t, tc.method, fields["method"])
		assert.Equal(t, tc.path, fields["uri"])
		assert.EqualValues(t, tc.want, fields["status"])
	}
}
