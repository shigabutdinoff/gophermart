package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func TestRouter_AuthRouteScopeAndPublicAccess(t *testing.T) {
	_, httpServer := newTestServer(t, zap.NewNop(), config.Default())
	validBody := `{"login":"user","password":"password"}`

	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodPost, "/api/user/register", validBody, http.StatusInternalServerError},
		{http.MethodPost, "/api/user/login", validBody, http.StatusInternalServerError},
		{http.MethodPost, "/api/user/orders", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/user/orders", "", http.StatusUnauthorized},
		{http.MethodHead, "/api/user/orders", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/user/balance", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/user/balance/withdraw", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/user/withdrawals", "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		request, err := http.NewRequest(
			tt.method,
			httpServer.URL+tt.path,
			strings.NewReader(tt.body),
		)
		require.NoError(t, err)
		if tt.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}

		response, err := httpServer.Client().Do(request)
		require.NoError(t, err)
		response.Body.Close()
		assert.Equal(t, tt.status, response.StatusCode, tt.method+" "+tt.path)
	}
}

func TestRouter_MiddlewareRejectionsHaveEmptyBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
	}{
		{
			name: "неподдерживаемая кодировка",
			body: `{"login":"user","password":"password"}`,
			headers: map[string]string{
				"Content-Type":     "application/json",
				"Content-Encoding": "deflate",
			},
			status: http.StatusUnsupportedMediaType,
		},
		{
			name: "повреждённый gzip",
			body: "not-gzip",
			headers: map[string]string{
				"Content-Type":     "application/json",
				"Content-Encoding": "gzip",
			},
			status: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mustNew(t, zap.NewNop(), config.Default())
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/user/login",
				strings.NewReader(tt.body),
			)
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()

			server.router.ServeHTTP(response, request)

			require.Equal(t, tt.status, response.Code)
			assert.Empty(t, response.Body.Bytes())
			assert.Empty(t, response.Header().Get("Content-Type"))
		})
	}
}
