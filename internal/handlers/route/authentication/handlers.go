package authentication

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route"
)

// Тексты ответов маршрутов аутентификации.
const (
	MessageLoginTaken         = "Логин уже занят"
	MessageInvalidCredentials = "Неверная пара логин/пароль"
)

// Пути маршрутов аутентификации, заданные ТЗ.
const (
	registerPath = "/api/user/register"
	loginPath    = "/api/user/login"
)

// CredentialsFunc заводит учётную запись по нормализованным данным.
type CredentialsFunc func(context.Context, auth.Credentials) (auth.IssuedToken, error)

type Deps struct {
	Register CredentialsFunc
	Login    CredentialsFunc
}

type Options = route.Options

type registerInput struct {
	Body registerBody
}

type loginInput struct {
	Body loginBody
}

// authOutput несёт токен заголовком и cookie, тело успеха задано ТЗ пустым.
type authOutput struct {
	Authorization string      `header:"Authorization"`
	SetCookie     http.Cookie `header:"Set-Cookie"`
}

// RegisterRoutes публикует маршруты аутентификации как huma-операции.
func RegisterRoutes(api huma.API, logger *zap.Logger, deps Deps, options Options) {
	registerRegistrationRoute(api, logger, deps.Register, options)
	registerLoginRoute(api, logger, deps.Login, options)
}

func registerRegistrationRoute(api huma.API, logger *zap.Logger, register CredentialsFunc, options Options) {
	huma.Register(api, huma.Operation{
		OperationID: "register-user",
		Method:      http.MethodPost,
		Path:        registerPath,
		Summary:     "Регистрация пользователя",
		Metadata:    apiconfig.ValidationErrorsAsBadRequest(),
		// пустое тело успеха не должно превращаться в 204
		DefaultStatus: http.StatusOK,
		Middlewares:   options.Middlewares,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusConflict,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, in *registerInput) (*authOutput, error) {
		credentials := auth.NormalizeCredentials(in.Body.Login, in.Body.Password)
		token, err := register(ctx, credentials)
		switch {
		case err == nil:
			return authenticated(token), nil
		case errors.Is(err, auth.ErrLoginTaken):
			return nil, huma.Error409Conflict(MessageLoginTaken)
		default:
			return nil, route.InternalError(logger, "Не удалось зарегистрировать пользователя", err)
		}
	})
}

func registerLoginRoute(api huma.API, logger *zap.Logger, login CredentialsFunc, options Options) {
	huma.Register(api, huma.Operation{
		OperationID:   "login-user",
		Method:        http.MethodPost,
		Path:          loginPath,
		Summary:       "Вход пользователя",
		Metadata:      apiconfig.ValidationErrorsAsBadRequest(),
		DefaultStatus: http.StatusOK,
		Middlewares:   options.Middlewares,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, in *loginInput) (*authOutput, error) {
		credentials := auth.NormalizeCredentials(in.Body.Login, in.Body.Password)
		token, err := login(ctx, credentials)
		switch {
		case err == nil:
			return authenticated(token), nil
		case errors.Is(err, auth.ErrInvalidCredentials):
			return nil, huma.Error401Unauthorized(MessageInvalidCredentials)
		default:
			return nil, route.InternalError(logger, "Не удалось выполнить вход пользователя", err)
		}
	})
}

// authenticated отдаёт выпущенный токен заголовком и session-кукой.
func authenticated(token auth.IssuedToken) *authOutput {
	return &authOutput{
		Authorization: "Bearer " + token.Value,
		SetCookie: http.Cookie{
			Name:     authorization.SessionCookieName,
			Value:    token.Value,
			Path:     "/",
			Expires:  token.ExpiresAt,
			MaxAge:   int(auth.TokenTTL / time.Second),
			HttpOnly: true,
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
		},
	}
}
