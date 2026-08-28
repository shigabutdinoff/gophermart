package server

import (
	"net/http"
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
		{http.MethodHead, "/api/user/orders", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/user/balance", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/user/balance/withdraw", "", http.StatusUnauthorized},
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
