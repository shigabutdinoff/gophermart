package authentication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

func TestLogin_UnknownLoginAndWrongPasswordShareBody(t *testing.T) {
	deps := loginReturning(auth.LoginResult{}, auth.ErrInvalidCredentials, nil)
	handler := newRouter(zap.NewNop(), deps, testBodyLimit)

	bodies := make([]string, 0, 2)
	for _, login := range []string{"unknown", "known"} {
		response := serve(handler, loginPath, "application/json",
			`{"login":"`+login+`","password":"password"}`)

		require.Equal(t, http.StatusUnauthorized, response.Code)
		bodies = append(bodies, response.Body.String())
	}

	assert.Equal(t, bodies[0], bodies[1])
}

func TestLogin_MapsAllOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		result  auth.LoginResult
		err     error
		status  int
		message string
		retry   string
	}{
		{"success", auth.LoginResult{Token: handlerTestToken}, nil, http.StatusOK, "", ""},
		{"invalid", auth.LoginResult{}, auth.ErrInvalidCredentials, http.StatusUnauthorized, MessageInvalidCredentials, ""},
		{"limited", auth.LoginResult{RetryAfter: 7}, auth.ErrRateLimited, http.StatusTooManyRequests, MessageTooManyAttempts, "7"},
		{"internal", auth.LoginResult{}, errors.New("storage"), http.StatusInternalServerError, message.Internal, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := serveLogin(
				loginReturning(tt.result, tt.err, nil),
				"application/json",
				`{"login":"user","password":"password"}`,
			)

			require.Equal(t, tt.status, response.Code)
			assert.Equal(t, tt.retry, response.Header().Get("Retry-After"))
			if tt.status == http.StatusOK {
				assert.Equal(t, "Bearer signed-token", response.Header().Get("Authorization"))
				return
			}
			assertProblem(t, response, tt.status, tt.message)
		})
	}
}

// Ключ лимитера строится по адресу соединения, заголовкам прокси не верим.
func TestLogin_TakesClientAddressFromConnection(t *testing.T) {
	var seen netip.Addr
	deps := Deps{
		Login: func(_ context.Context, _ auth.Credentials, client netip.Addr) (auth.LoginResult, error) {
			seen = client
			return auth.LoginResult{Token: handlerTestToken}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, loginPath,
		strings.NewReader(`{"login":"user","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	request.RemoteAddr = "192.0.2.10:4321"

	newRouter(zap.NewNop(), deps, testBodyLimit).ServeHTTP(httptest.NewRecorder(), request)

	assert.Equal(t, "192.0.2.10", seen.String())
}

// Форма IPv4-в-IPv6 сводится к своему IPv4, иначе ключ лимитера раздвоится.
func TestLogin_UnmapsIPv4MappedAddress(t *testing.T) {
	var seen netip.Addr
	deps := Deps{
		Login: func(_ context.Context, _ auth.Credentials, client netip.Addr) (auth.LoginResult, error) {
			seen = client
			return auth.LoginResult{Token: handlerTestToken}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, loginPath,
		strings.NewReader(`{"login":"user","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "[::ffff:192.0.2.10]:4321"

	newRouter(zap.NewNop(), deps, testBodyLimit).ServeHTTP(httptest.NewRecorder(), request)

	assert.Equal(t, "192.0.2.10", seen.String())
}
