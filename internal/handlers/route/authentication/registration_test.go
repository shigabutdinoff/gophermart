package authentication

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route/message"
)

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

func TestRegister_OversizedBodyAnswers413(t *testing.T) {
	const limit = 64
	calls := 0
	deps := registerReturning(handlerTestToken, nil, &calls)
	body := `{"login":"user","password":"password"}` + strings.Repeat(" ", limit)

	response := serve(
		newRouter(zap.NewNop(), deps, limit),
		registerPath, "application/json", body,
	)

	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Zero(t, calls)
}

// Тело ровно в границу лимита проходит, 413 даёт только превышение.
func TestRegister_BodyAtLimitPasses(t *testing.T) {
	body := `{"login":"user","password":"password"}`
	calls := 0
	deps := registerReturning(handlerTestToken, nil, &calls)

	response := serve(
		newRouter(zap.NewNop(), deps, int64(len(body))),
		registerPath, "application/json", body,
	)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 1, calls)
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
