package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func TestRouter_LivenessAnswersWithoutDatabase(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	resp, err := srv.Client().Get(srv.URL + "/live")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, ".", string(body))
}

func TestRouter_LivenessIsExactGETRoute(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())

	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "HEAD отвечает пробой живости", method: http.MethodHead, path: "/live", status: http.StatusOK},
		{name: "путь сравнивается без учёта регистра", method: http.MethodGet, path: "/LIVE", status: http.StatusOK},
		{name: "чужой метод отклоняется", method: http.MethodPost, path: "/live", status: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, http.NoBody)

			server.router.ServeHTTP(response, request)

			assert.Equal(t, tt.status, response.Code)
		})
	}
}

func TestRouter_LivenessBypassesResponseCompression(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/live", http.NoBody)
	request.Header.Set("Accept-Encoding", "gzip")

	server.router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/plain", response.Header().Get("Content-Type"))
	assert.Empty(t, response.Header().Get("Content-Encoding"))
	assert.Equal(t, ".", response.Body.String())
}

func TestRouter_ReadinessRequiresDatabase(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	for _, path := range []string{"/ready", "/ping"} {
		t.Run(path, func(t *testing.T) {
			resp, err := srv.Client().Get(srv.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		})
	}
}

func TestRouter_ReadinessAndPingShareHandler(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())

	ready := httptest.NewRecorder()
	server.router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	ping := httptest.NewRecorder()
	server.router.ServeHTTP(ping, httptest.NewRequest(http.MethodGet, "/ping", nil))

	assert.Equal(t, ready.Code, ping.Code)
	assert.Equal(t, ready.Header().Get("Content-Type"), ping.Header().Get("Content-Type"))
	// отчёт несёт отметку времени проверки, сверяется его состав
	assert.Equal(t, reportWithoutTimestamps(t, ready), reportWithoutTimestamps(t, ping))
}

// reportWithoutTimestamps убирает из отчёта готовности отметки времени.
func reportWithoutTimestamps(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var report map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &report))
	details, _ := report["details"].(map[string]any)
	for _, value := range details {
		check, _ := value.(map[string]any)
		delete(check, "timestamp")
	}

	return report
}
