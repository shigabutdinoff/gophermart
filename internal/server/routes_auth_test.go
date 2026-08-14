package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
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
		{http.MethodGet, "/ping", "", http.StatusServiceUnavailable},
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

func TestRouter_OrderAuthorizationPrecedesContentTypeFilter(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())
	tokens, err := auth.NewJWTManager([]byte(testJWTSecret))
	require.NoError(t, err)
	issued, err := tokens.Issue(42)
	require.NoError(t, err)

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:       "missing token is rejected before content type",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "valid token reaches content type without a user lookup",
			authorization: "Bearer " + issued.Value,
			wantStatus:    http.StatusUnsupportedMediaType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/user/orders",
				strings.NewReader("12345678903"),
			)
			request.Header.Set("Content-Type", "application/json")
			if tt.authorization != "" {
				request.Header.Set("Authorization", tt.authorization)
			}
			response := httptest.NewRecorder()

			server.router.ServeHTTP(response, request)

			assert.Equal(t, tt.wantStatus, response.Code)
		})
	}
}

func TestRouter_AuthContentTypeUsesChiSemantics(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())
	validBody := `{"login":"user","password":"password"}`
	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		contentTypes []string
		status       int
	}{
		{"exact json reaches handler", http.MethodPost, "/api/user/register", validBody, []string{"application/json"}, http.StatusInternalServerError},
		{"malformed parameters reach handler", http.MethodPost, "/api/user/login", validBody, []string{`application/json; charset="`}, http.StatusInternalServerError},
		{"first header value wins", http.MethodPost, "/api/user/login", validBody, []string{"application/json", "text/plain"}, http.StatusInternalServerError},
		{"vendor json rejected", http.MethodPost, "/api/user/register", validBody, []string{"application/vnd.gophermart+json"}, http.StatusUnsupportedMediaType},
		{"other type rejected", http.MethodPost, "/api/user/login", validBody, []string{"text/json"}, http.StatusUnsupportedMediaType},
		{"missing type rejected", http.MethodPost, "/api/user/login", validBody, nil, http.StatusUnsupportedMediaType},
		{"ping is not filtered", http.MethodGet, "/ping", "body", []string{"text/plain"}, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			for _, contentType := range tt.contentTypes {
				request.Header.Add("Content-Type", contentType)
			}
			response := httptest.NewRecorder()

			server.router.ServeHTTP(response, request)

			assert.Equal(t, tt.status, response.Code)
			if tt.status == http.StatusUnsupportedMediaType {
				assert.Empty(t, response.Body.Bytes())
			}
		})
	}
}

func TestRouterDoesNotRateLimitRegistrationOrInternalLoginFailures(t *testing.T) {
	_, srv := newTestServer(t, zap.NewNop(), config.Default())
	body := `{"login":"user","password":"password"}`

	post := func(path string) *http.Response {
		request, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err := srv.Client().Do(request)
		require.NoError(t, err)
		return response
	}

	for _, path := range []string{"/api/user/login", "/api/user/register"} {
		for range auth.MaxLoginAttempts + 1 {
			response := post(path)
			assert.Equal(t, http.StatusInternalServerError, response.StatusCode)
			assert.Empty(t, response.Header.Get("Retry-After"))
			response.Body.Close()
		}
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
			name:    "неподдерживаемый Content-Type",
			body:    `{"login":"user","password":"password"}`,
			headers: map[string]string{"Content-Type": "text/plain"},
			status:  http.StatusUnsupportedMediaType,
		},
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
