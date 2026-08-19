package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

var handlerTestToken = auth.IssuedToken{
	Value:     "signed-token",
	IssuedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	ExpiresAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
}

// newRouter собирает маршруты так же, как это делает сервер.
func newRouter(logger *zap.Logger, deps Deps) http.Handler {
	router := chi.NewRouter()
	api := humachi.New(router, apiconfig.New())
	RegisterRoutes(api, logger, deps, nil)

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
	return serve(newRouter(zap.NewNop(), deps), "/api/user/register", contentType, body)
}

func serveLogin(deps Deps, contentType, body string) *httptest.ResponseRecorder {
	return serve(newRouter(zap.NewNop(), deps), "/api/user/login", contentType, body)
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
func loginReturning(token auth.IssuedToken, err error, calls *int) Deps {
	return Deps{
		Login: func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
			if calls != nil {
				*calls++
			}
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

func TestHandlersLogInternalErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func(*zap.Logger, error) *httptest.ResponseRecorder
	}{
		{
			name: "register",
			run: func(logger *zap.Logger, err error) *httptest.ResponseRecorder {
				deps := registerReturning(auth.IssuedToken{}, err, nil)
				return serve(newRouter(logger, deps),
					"/api/user/register", "application/json",
					`{"login":"user","password":"password"}`)
			},
		},
		{
			name: "login",
			run: func(logger *zap.Logger, err error) *httptest.ResponseRecorder {
				deps := loginReturning(auth.IssuedToken{}, err, nil)
				return serve(newRouter(logger, deps),
					"/api/user/login", "application/json",
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
		newRouter(logger, registerReturning(auth.IssuedToken{}, auth.ErrLoginTaken, nil)),
		"/api/user/register", "application/json", `{"login":"user","password":"password"}`,
	)
	serve(
		newRouter(logger, loginReturning(auth.IssuedToken{}, auth.ErrInvalidCredentials, nil)),
		"/api/user/login", "application/json", `{"login":"user","password":"password"}`,
	)

	assert.Zero(t, logs.Len())
}

func TestRegister_RejectsDataAfterJSONObject(t *testing.T) {
	calls := 0

	response := serveRegister(
		registerReturning(handlerTestToken, nil, &calls),
		"application/json",
		`{"login":"user","password":"password"}{"extra":1}`,
	)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, calls)
}

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

func TestRegister_NamesViolatedCredentialsField(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		location string
	}{
		{
			name:     "длинный login",
			body:     `{"login":"` + strings.Repeat("l", 256) + `","password":"password"}`,
			location: "body.login",
		},
		{
			name:     "нулевой символ в login",
			body:     `{"login":"user\u0000suffix","password":"password"}`,
			location: "body.login",
		},
		{
			name:     "логин из одних пробелов",
			body:     `{"login":"   ","password":"password"}`,
			location: "body.login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0

			response := serveRegister(registerReturning(handlerTestToken, nil, &calls), "application/json", tt.body)

			assertProblem(t, response, http.StatusBadRequest, "")
			assert.Contains(t, problemLocations(t, response), tt.location)
			assert.Zero(t, calls)
		})
	}
}

func TestRegister_ValidMediaTypesNormalizeAndIgnoreUnknownFields(t *testing.T) {
	mediaTypes := []string{
		"application/json",
		"application/json; charset=utf-8",
	}
	for _, mediaType := range mediaTypes {
		t.Run(mediaType, func(t *testing.T) {
			calls := 0
			deps := Deps{
				Register: func(_ context.Context, credentials auth.Credentials) (auth.IssuedToken, error) {
					calls++
					assert.Equal(t, "юзер", credentials.Login)
					assert.Equal(t, " password ", credentials.Password)
					return handlerTestToken, nil
				},
			}

			response := serveRegister(
				deps,
				mediaType,
				`{"login":" ЮЗЕР ","password":" password ","password_confirmation":"ignored"}`,
			)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, 1, calls)
			assert.Equal(t, "Bearer signed-token", response.Header().Get("Authorization"))
			result := response.Result()
			defer result.Body.Close()
			cookies := result.Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			assert.Equal(t, authorization.SessionCookieName, cookie.Name)
			assert.Equal(t, "signed-token", cookie.Value)
			assert.Equal(t, "/", cookie.Path)
			assert.True(t, cookie.HttpOnly)
			assert.False(t, cookie.Secure)
			assert.Empty(t, cookie.Domain)
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			assert.Equal(t, int(auth.TokenTTL/time.Second), cookie.MaxAge)
			assert.Equal(t, handlerTestToken.ExpiresAt.UTC(), cookie.Expires.UTC())
		})
	}
}

func TestRegister_CookieMaxAgeUsesTokenTTLAndExpiresUsesIssuedToken(t *testing.T) {
	shortToken := auth.IssuedToken{Value: "short-token", ExpiresAt: time.Now().Add(5 * time.Minute)}

	response := serveRegister(
		registerReturning(shortToken, nil, nil),
		"application/json",
		`{"login":"user","password":"password"}`,
	)

	result := response.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, int(auth.TokenTTL/time.Second), cookies[0].MaxAge)
	assert.WithinDuration(t, shortToken.ExpiresAt, cookies[0].Expires, time.Second)
}

func TestRegister_InvalidRequestsStopBeforeAction(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{"malformed json", "application/json", `{"login":`, http.StatusBadRequest},
		{"wrong field type", "application/json", `{"login":1,"password":"password"}`, http.StatusBadRequest},
		{"missing field", "application/json", `{"login":"user"}`, http.StatusBadRequest},
		{"escaped NUL in login", "application/json", `{"login":"user\u0000suffix","password":"password"}`, http.StatusBadRequest},
		{"login of spaces", "application/json", `{"login":"   ","password":"password"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0

			response := serveRegister(registerReturning(handlerTestToken, nil, &calls), tt.contentType, tt.body)

			require.Equal(t, tt.status, response.Code)
			assertProblem(t, response, tt.status, "")
			assert.Zero(t, calls)
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Values("Set-Cookie"))
		})
	}
}

func TestRegister_MapsErrorsWithoutToken(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"login taken", auth.ErrLoginTaken, http.StatusConflict, MessageLoginTaken},
		{"internal", errors.New("storage"), http.StatusInternalServerError, message.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := serveRegister(
				registerReturning(auth.IssuedToken{}, tt.err, nil),
				"application/json",
				`{"login":"user","password":"password"}`,
			)

			assertProblem(t, response, tt.status, tt.message)
		})
	}
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

// ТЗ допускает для маршрутов аутентификации только 200, 400, 401, 409 и 500.
func TestRoutes_StatusesStayWithinSpecification(t *testing.T) {
	allowed := map[int]bool{
		http.StatusOK:                  true,
		http.StatusBadRequest:          true,
		http.StatusUnauthorized:        true,
		http.StatusConflict:            true,
		http.StatusInternalServerError: true,
	}
	bodies := []string{
		`{"login":"user","password":"password"}`,
		`{"login":"   ","password":"password"}`,
		`{"login":1,"password":"password"}`,
		`{"login":"user"}`,
		`{`,
	}

	for _, body := range bodies {
		for _, path := range []string{"/api/user/register", "/api/user/login"} {
			deps := Deps{
				Register: func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
					return handlerTestToken, nil
				},
				Login: func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
					return handlerTestToken, nil
				},
			}
			response := serve(newRouter(zap.NewNop(), deps), path, "application/json", body)

			assert.True(t, allowed[response.Code],
				"%s с телом %s ответил %d вне контракта ТЗ", path, body, response.Code)
		}
	}
}

// Понижение 422 действует только на путях этого пакета.
// Номеру заказа неверного формата ТЗ предписывает именно 422.
func TestRoutes_OtherOperationsKeepUnprocessableEntity(t *testing.T) {
	type orderBody struct {
		Number string `json:"number" minLength:"5"`
	}
	type orderInput struct {
		Body orderBody
	}

	router := chi.NewRouter()
	api := humachi.New(router, apiconfig.New())
	RegisterRoutes(api, zap.NewNop(), registerReturning(handlerTestToken, nil, nil), nil)
	huma.Register(api, huma.Operation{
		OperationID: "upload-order",
		Method:      http.MethodPost,
		Path:        "/api/user/orders",
	}, func(context.Context, *orderInput) (*struct{}, error) {
		return nil, nil
	})

	response := serve(router, "/api/user/orders", "application/json", `{"number":"1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
}

// Логин в 255 символов с пробелами по краям валиден.
// Границу меряет нормализованное значение, а не сырая строка из тела.
func TestRegister_MeasuresLoginAfterTrimming(t *testing.T) {
	login := strings.Repeat("я", 255)
	calls := 0

	response := serveRegister(
		registerReturning(handlerTestToken, nil, &calls),
		"application/json",
		`{"login":"  `+login+`  ","password":"password"}`,
	)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 1, calls)
}

// В сервис уходит нормализованный логин.
// Пробелы по краям не заводят отдельную учётную запись.
func TestRegister_NormalizesLoginBeforeService(t *testing.T) {
	var seen auth.Credentials
	deps := Deps{Register: func(_ context.Context, credentials auth.Credentials) (auth.IssuedToken, error) {
		seen = credentials
		return handlerTestToken, nil
	}}

	response := serveRegister(deps, "application/json", `{"login":" ЮЗЕР ","password":"password"}`)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "юзер", seen.Login)
}

// Служебные маршруты фреймворка наружу не публикуются.
func TestRoutes_NoFrameworkServiceEndpoints(t *testing.T) {
	handler := newRouter(zap.NewNop(), registerReturning(handlerTestToken, nil, nil))

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/docs", "/schemas/ErrorModel.json"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}

// Тело ошибки не раскрывает внутренние адреса схем.
func TestRegister_ProblemBodyHasNoSchemaLink(t *testing.T) {
	response := serveRegister(
		registerReturning(handlerTestToken, nil, nil),
		"application/json",
		`{"login":"   ","password":"password"}`,
	)

	require.Equal(t, http.StatusBadRequest, response.Code)
	assert.NotContains(t, response.Body.String(), "$schema")
}
