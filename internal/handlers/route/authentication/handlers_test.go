package authentication

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

var handlerTestToken = auth.IssuedToken{
	Value:     "signed-token",
	IssuedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	ExpiresAt: time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC),
}

func serve(
	handler http.Handler,
	contentType string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.RemoteAddr = "192.0.2.10:4321"
	response := httptest.NewRecorder()
	handler = middleware.AllowContentType("application/json")(handler)
	handler.ServeHTTP(response, request)
	return response
}

func assertEmptyResponse(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	assert.Equal(t, status, response.Code)
	assert.Empty(t, response.Body.Bytes())
}

// assertMessageResponse проверяет статус и тело-конверт с одним сообщением.
func assertMessageResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	message string,
) {
	t.Helper()
	assert.Equal(t, status, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"message":"`+message+`"}`, response.Body.String())
}

func TestHandlersLogInternalErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*zap.Logger, error) http.HandlerFunc
	}{
		{
			name: "register",
			handler: func(logger *zap.Logger, err error) http.HandlerFunc {
				return Register(logger, time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
					return auth.IssuedToken{}, err
				})
			},
		},
		{
			name: "login",
			handler: func(logger *zap.Logger, err error) http.HandlerFunc {
				return Login(logger, time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
					return auth.IssuedToken{}, err
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.ErrorLevel)

			response := serve(
				tt.handler(zap.New(core), errors.New("create user: storage is down")),
				"application/json",
				`{"login":"user","password":"password"}`,
			)

			assertMessageResponse(t, response, http.StatusInternalServerError, MessageInternalError)
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
		Register(logger, time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
			return auth.IssuedToken{}, auth.ErrLoginTaken
		}),
		"application/json",
		`{"login":"user","password":"password"}`,
	)
	serve(
		Login(logger, time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
			return auth.IssuedToken{}, auth.ErrInvalidCredentials
		}),
		"application/json",
		`{"login":"user","password":"password"}`,
	)

	assert.Zero(t, logs.Len())
}

func TestRegister_RejectsDataAfterJSONObject(t *testing.T) {
	calls := 0
	handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		calls++
		return auth.IssuedToken{Value: "token-value"}, nil
	})
	response := serve(
		handler,
		"application/json",
		`{"login":"user","password":"password"}{"extra":1}`,
	)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, calls)
}

func TestRegister_OversizedTailAnswers413(t *testing.T) {
	const limit = 64
	calls := 0
	handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		calls++
		return auth.IssuedToken{Value: "token-value"}, nil
	})
	body := `{"login":"user","password":"password"}` + strings.Repeat(" ", limit)
	request := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(body))
	response := httptest.NewRecorder()
	request.Body = http.MaxBytesReader(response, request.Body, limit)

	handler(response, request)

	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Zero(t, calls)
	assert.JSONEq(t, `{"message":"`+MessageBodyTooLarge+`"}`, response.Body.String())
}

func TestLogin_UnknownLoginAndWrongPasswordShareBody(t *testing.T) {
	handler := Login(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		return auth.IssuedToken{}, auth.ErrInvalidCredentials
	})

	bodies := make([]string, 0, 2)
	for _, login := range []string{"unknown", "known"} {
		response := serve(
			handler,
			"application/json",
			`{"login":"`+login+`","password":"password"}`,
		)

		require.Equal(t, http.StatusUnauthorized, response.Code)
		bodies = append(bodies, response.Body.String())
	}

	assert.Equal(t, bodies[0], bodies[1])
}

func TestRegister_NamesViolatedCredentialsRule(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		message string
	}{
		{
			name:    "короткий пароль",
			body:    `{"login":"user","password":"short"}`,
			message: "Пароль короче 8 символов",
		},
		{
			name:    "длинный login",
			body:    `{"login":"` + strings.Repeat("l", 256) + `","password":"password"}`,
			message: "Логин длиннее 255 символов",
		},
	}

	messages := make([]string, 0, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
				return auth.IssuedToken{}, nil
			})
			response := serve(handler, "application/json", tt.body)

			assertMessageResponse(t, response, http.StatusBadRequest, tt.message)
			messages = append(messages, response.Body.String())
		})
	}

	require.Len(t, messages, 2)
	assert.NotEqual(t, messages[0], messages[1])
}

func TestRegister_ValidMediaTypesNormalizeAndIgnoreUnknownFields(t *testing.T) {
	mediaTypes := []string{
		"application/json",
		"application/json; charset=utf-8",
		`application/json; charset="`,
		" Application/JSON ; ignored",
	}
	for _, mediaType := range mediaTypes {
		t.Run(mediaType, func(t *testing.T) {
			calls := 0
			handler := Register(zap.NewNop(), time.Now, func(_ context.Context, credentials auth.Credentials) (auth.IssuedToken, error) {
				calls++
				assert.Equal(t, "юзер", credentials.Login)
				assert.Equal(t, " password ", credentials.Password)
				return handlerTestToken, nil
			})

			response := serve(
				handler,
				mediaType,
				`{"login":" ЮЗЕР ","password":" password ","password_confirmation":"ignored"}`,
			)

			assertMessageResponse(t, response, http.StatusOK, MessageRegistered)
			assert.Equal(t, 1, calls)
			assert.Equal(t, "Bearer signed-token", response.Header().Get("Authorization"))
			result := response.Result()
			defer result.Body.Close()
			cookies := result.Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			assert.Equal(t, SessionCookieName, cookie.Name)
			assert.Equal(t, "signed-token", cookie.Value)
			assert.Equal(t, "/", cookie.Path)
			assert.True(t, cookie.HttpOnly)
			assert.False(t, cookie.Secure)
			assert.Empty(t, cookie.Domain)
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			assert.Negative(t, cookie.MaxAge, "истёкший токен не продлевает куку")
			assert.Equal(t, handlerTestToken.ExpiresAt, cookie.Expires)
		})
	}
}

func TestRegister_CookieLifetimeFollowsIssuedToken(t *testing.T) {
	shortToken := auth.IssuedToken{Value: "short-token", ExpiresAt: time.Now().Add(5 * time.Minute)}
	handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		return shortToken, nil
	})

	response := serve(handler, "application/json", `{"login":"user","password":"password"}`)

	result := response.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	require.Len(t, cookies, 1)
	assert.InDelta(t, time.Until(shortToken.ExpiresAt).Seconds(), cookies[0].MaxAge, 2)
	assert.WithinDuration(t, shortToken.ExpiresAt, cookies[0].Expires, time.Second)
}

func TestRegister_CookieLifetimeFollowsInjectedClock(t *testing.T) {
	now := func() time.Time { return handlerTestToken.IssuedAt }
	handler := Register(zap.NewNop(), now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		return handlerTestToken, nil
	})

	response := serve(handler, "application/json", `{"login":"user","password":"password"}`)

	result := response.Result()
	defer result.Body.Close()
	cookies := result.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, int(time.Hour.Seconds()), cookies[0].MaxAge)
}

func TestRegister_InvalidRequestsStopBeforeAction(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		message     string
	}{
		{"missing content type", "", `{"login":"user","password":"password"}`, http.StatusUnsupportedMediaType, ""},
		{"vendor json", "application/vnd.gophermart+json", `{"login":"user","password":"password"}`, http.StatusUnsupportedMediaType, ""},
		{"unsupported content type", "text/json", `{"login":"user","password":"password"}`, http.StatusUnsupportedMediaType, ""},
		{"malformed base type", `application/"`, `{"login":"user","password":"password"}`, http.StatusUnsupportedMediaType, ""},
		{"malformed json", "application/json", `{"login":`, http.StatusBadRequest, MessageBadRequest},
		{"wrong field type", "application/json", `{"login":1,"password":"password"}`, http.StatusBadRequest, MessageBadRequest},
		{"missing field", "application/json", `{"login":"user"}`, http.StatusBadRequest, "Пароль короче 8 символов"},
		{"invalid credentials", "application/json", `{"login":"user","password":"short"}`, http.StatusBadRequest, "Пароль короче 8 символов"},
		{"escaped NUL in login", "application/json", `{"login":"user\u0000suffix","password":"password"}`, http.StatusBadRequest, "Логин содержит недопустимый символ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
				calls++
				return handlerTestToken, nil
			})

			response := serve(handler, tt.contentType, tt.body)

			if tt.message == "" {
				assertEmptyResponse(t, response, tt.status)
			} else {
				assertMessageResponse(t, response, tt.status, tt.message)
			}
			assert.Zero(t, calls)
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Values("Set-Cookie"))
		})
	}
}

func TestRegister_UsesFirstContentTypeValue(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		status  int
		calls   int
		message string
	}{
		{"first accepted", []string{"application/json", "text/plain"}, http.StatusOK, 1, MessageRegistered},
		{"first rejected", []string{"text/plain", "application/json"}, http.StatusUnsupportedMediaType, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
				calls++
				return handlerTestToken, nil
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/",
				strings.NewReader(`{"login":"user","password":"password"}`),
			)
			for _, value := range tt.values {
				request.Header.Add("Content-Type", value)
			}
			response := httptest.NewRecorder()

			middleware.AllowContentType("application/json")(handler).ServeHTTP(response, request)

			if tt.message == "" {
				assertEmptyResponse(t, response, tt.status)
			} else {
				assertMessageResponse(t, response, tt.status, tt.message)
			}
			assert.Equal(t, tt.calls, calls)
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
		{"internal", errors.New("storage"), http.StatusInternalServerError, MessageInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Register(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
				return auth.IssuedToken{}, tt.err
			})

			response := serve(
				handler,
				"application/json",
				`{"login":"user","password":"password"}`,
			)

			assertMessageResponse(t, response, tt.status, tt.message)
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Values("Set-Cookie"))
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
		{"success", handlerTestToken, nil, http.StatusOK, MessageLoggedIn},
		{"invalid", auth.IssuedToken{}, auth.ErrInvalidCredentials, http.StatusUnauthorized, MessageInvalidCredentials},
		{"internal", auth.IssuedToken{}, errors.New("storage"), http.StatusInternalServerError, MessageInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Login(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
				return tt.token, tt.err
			})

			response := serve(
				handler,
				"application/json",
				`{"login":"user","password":"password"}`,
			)

			assertMessageResponse(t, response, tt.status, tt.message)
			if tt.status == http.StatusOK {
				assert.Equal(t, "Bearer signed-token", response.Header().Get("Authorization"))
				result := response.Result()
				defer result.Body.Close()
				require.Len(t, result.Cookies(), 1)
			} else {
				assert.Empty(t, response.Header().Get("Authorization"))
				assert.Empty(t, response.Header().Values("Set-Cookie"))
			}
		})
	}
}

func TestLogin_InvalidRequestDoesNotCallPipeline(t *testing.T) {
	calls := 0
	handler := Login(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		calls++
		return auth.IssuedToken{}, nil
	})

	response := serve(handler, "application/json", `{"login":"user"}`)

	assertMessageResponse(t, response, http.StatusBadRequest, "Пароль не может быть пустым")
	assert.Zero(t, calls)
}

func TestLoginRejectsEscapedNULBeforeRepository(t *testing.T) {
	users := &countingLoginRepository{}
	service, err := auth.NewLoginService(zap.NewNop(), users, auth.Argon2Passwords{}, &countingTokenIssuer{})
	require.NoError(t, err)

	response := serve(
		Login(zap.NewNop(), time.Now, service.Login),
		"application/json",
		`{"login":"user\u0000suffix","password":"password"}`,
	)

	assertMessageResponse(t, response, http.StatusBadRequest, "Логин содержит недопустимый символ")
	assert.Zero(t, users.findCalls)
}

type countingLoginRepository struct {
	findCalls int
}

func (*countingLoginRepository) Create(context.Context, string, string) (auth.User, error) {
	return auth.User{}, nil
}

func (r *countingLoginRepository) FindByLogin(context.Context, string) (auth.User, error) {
	r.findCalls++
	return auth.User{}, auth.ErrUserNotFound
}

type countingTokenIssuer struct{}

func (*countingTokenIssuer) Issue(int64) (auth.IssuedToken, error) {
	return auth.IssuedToken{}, nil
}

func TestErrorBodiesCarryOnlyMessage(t *testing.T) {
	handler := Login(zap.NewNop(), time.Now, func(context.Context, auth.Credentials) (auth.IssuedToken, error) {
		return auth.IssuedToken{}, auth.ErrInvalidCredentials
	})
	response := serve(
		handler,
		"application/json",
		`{"login":"user","password":"password"}`,
	)

	result := response.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)

	require.NoError(t, err)
	assert.JSONEq(t, `{"message":"`+MessageInvalidCredentials+`"}`, string(body))
	assert.NotContains(t, string(body), "password")
}
