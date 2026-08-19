package authentication

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

func TestLogin_UnknownLoginAndWrongPasswordShareBody(t *testing.T) {
	deps := loginReturning(auth.IssuedToken{}, auth.ErrInvalidCredentials, nil)
	handler := newRouter(zap.NewNop(), deps)

	bodies := make([]string, 0, 2)
	for _, login := range []string{"unknown", "known"} {
		response := serve(handler, "/api/user/login", "application/json",
			`{"login":"`+login+`","password":"password"}`)

		require.Equal(t, http.StatusUnauthorized, response.Code)
		bodies = append(bodies, response.Body.String())
	}

	assert.Equal(t, bodies[0], bodies[1])
}

func TestLogin_MapsAllOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		token   auth.IssuedToken
		err     error
		status  int
		message string
	}{
		{"success", handlerTestToken, nil, http.StatusOK, ""},
		{"invalid", auth.IssuedToken{}, auth.ErrInvalidCredentials, http.StatusUnauthorized, MessageInvalidCredentials},
		{"internal", auth.IssuedToken{}, errors.New("storage"), http.StatusInternalServerError, message.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := serveLogin(
				loginReturning(tt.token, tt.err, nil),
				"application/json",
				`{"login":"user","password":"password"}`,
			)

			require.Equal(t, tt.status, response.Code)
			if tt.status == http.StatusOK {
				assert.Equal(t, "Bearer signed-token", response.Header().Get("Authorization"))
				return
			}
			assertProblem(t, response, tt.status, tt.message)
		})
	}
}
