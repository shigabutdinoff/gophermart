package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
)

const testBodyLimit = 1 << 20

var handlerTestToken = auth.IssuedToken{
	Value:     "signed-token",
	IssuedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	ExpiresAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
}

// newRouter собирает маршруты так же, как это делает сервер.
func newRouter(logger *zap.Logger, deps Deps, bodyLimit int64) http.Handler {
	router := chi.NewRouter()
	api := apiconfig.NewAPI(router)
	RegisterRoutes(api, logger, deps, Options{
		BodyLimit: bodyLimit,
		Middlewares: huma.Middlewares{
			apiconfig.AllowContentType("application/json"),
		},
	})

	return router
}

func serve(handler http.Handler, path, contentType, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.RemoteAddr = "192.0.2.10:4321"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func serveRegister(deps Deps, contentType, body string) *httptest.ResponseRecorder {
	return serve(newRouter(zap.NewNop(), deps, testBodyLimit), registerPath, contentType, body)
}

func serveLogin(deps Deps, contentType, body string) *httptest.ResponseRecorder {
	return serve(newRouter(zap.NewNop(), deps, testBodyLimit), loginPath, contentType, body)
}

// registerReturning собирает зависимости регистрации с заданным исходом.
func registerReturning(token auth.IssuedToken, err error, calls *int) Deps {
	return Deps{
		Register: func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
			if calls != nil {
				*calls++
			}
			return token, err
		},
	}
}

// loginReturning делает то же для входа.
func loginReturning(token auth.IssuedToken, err error) Deps {
	return Deps{
		Login: func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
			return token, err
		},
	}
}

// assertProblem проверяет статус, тип и текст стандартного описания проблемы.
func assertProblem(t *testing.T, response *httptest.ResponseRecorder, status int, message string) {
	t.Helper()

	assert.Equal(t, status, response.Code)
	assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	if message != "" {
		assert.Contains(t, response.Body.String(), message)
	}
	assert.Empty(t, response.Header().Get("Authorization"))
	assert.Empty(t, response.Header().Values("Set-Cookie"))
}

// problemLocations собирает поля, названные описанием проблемы.
func problemLocations(t *testing.T, response *httptest.ResponseRecorder) []string {
	t.Helper()

	var problem struct {
		Errors []struct {
			Location string `json:"location"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))

	locations := make([]string, 0, len(problem.Errors))
	for _, detail := range problem.Errors {
		locations = append(locations, detail.Location)
	}

	return locations
}
