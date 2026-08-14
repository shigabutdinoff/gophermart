package authentication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

func TestHandlersLogInternalErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func(*zap.Logger, error) *httptest.ResponseRecorder
	}{
		{
			name: "register",
			run: func(logger *zap.Logger, err error) *httptest.ResponseRecorder {
				deps := registerReturning(auth.IssuedToken{}, err, nil)
				return serve(newRouter(logger, deps, testBodyLimit),
					registerPath, "application/json",
					`{"login":"user","password":"password"}`)
			},
		},
		{
			name: "login",
			run: func(logger *zap.Logger, err error) *httptest.ResponseRecorder {
				deps := loginReturning(auth.LoginResult{}, err, nil)
				return serve(newRouter(logger, deps, testBodyLimit),
					loginPath, "application/json",
					`{"login":"user","password":"password"}`)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.ErrorLevel)

			response := tt.run(zap.New(core), errors.New("create user: storage is down"))

			assertProblem(t, response, http.StatusInternalServerError, message.Internal)
			require.Equal(t, 1, logs.Len())
			entry := logs.All()[0]
			assert.Equal(t, zapcore.ErrorLevel, entry.Level)
			assert.Contains(t, entry.ContextMap()["error"], "storage is down")
			assert.NotContains(t, response.Body.String(), "storage is down")
		})
	}
}

func TestHandlersDoNotLogClientErrors(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)

	serve(
		newRouter(logger, registerReturning(auth.IssuedToken{}, auth.ErrLoginTaken, nil), testBodyLimit),
		registerPath, "application/json", `{"login":"user","password":"password"}`,
	)
	serve(
		newRouter(logger, loginReturning(auth.LoginResult{}, auth.ErrInvalidCredentials, nil), testBodyLimit),
		loginPath, "application/json", `{"login":"user","password":"password"}`,
	)

	assert.Zero(t, logs.Len())
}

// ТЗ допускает для маршрутов аутентификации только 200, 400, 401, 409 и 500.
func TestRoutes_StatusesStayWithinSpecification(t *testing.T) {
	allowed := map[int]bool{
		http.StatusOK:                    true,
		http.StatusBadRequest:            true,
		http.StatusUnauthorized:          true,
		http.StatusRequestEntityTooLarge: true,
		http.StatusConflict:              true,
		http.StatusInternalServerError:   true,
	}
	bodies := []string{
		`{"login":"user","password":"password"}`,
		`{"login":"   ","password":"password"}`,
		`{"login":1,"password":"password"}`,
		`{"login":"user"}`,
		`{`,
	}

	for _, body := range bodies {
		for _, path := range []string{registerPath, loginPath} {
			deps := Deps{
				Register: func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
					return handlerTestToken, nil
				},
				Login: func(context.Context, auth.Credentials, netip.Addr) (auth.LoginResult, error) {
					return auth.LoginResult{Token: handlerTestToken}, nil
				},
			}
			response := serve(newRouter(zap.NewNop(), deps, testBodyLimit), path, "application/json", body)

			assert.True(t, allowed[response.Code],
				"%s с телом %s ответил %d вне контракта ТЗ", path, body, response.Code)
		}
	}
}

// Служебные маршруты фреймворка наружу не публикуются.
func TestRoutes_NoFrameworkServiceEndpoints(t *testing.T) {
	handler := newRouter(zap.NewNop(), registerReturning(handlerTestToken, nil, nil), testBodyLimit)

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/docs", "/schemas/ErrorModel.json"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}
