package authentication

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
)

// Тексты ответов маршрутов аутентификации.
const (
	MessageLoginTaken         = "Логин уже занят"
	MessageInvalidCredentials = "Неверная пара логин/пароль"
	MessageInternalError      = "Внутренняя ошибка сервиса"
	MessageNullCharacter      = "Логин содержит недопустимый символ"
	MessageEmptyLogin         = "Логин не может быть пустым"
	MessageLongLogin          = "Логин слишком длинный"
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

// registerBody описывает вход регистрации, границы проверяет схема операции.
type registerBody struct {
	// лишние поля тела игнорируются, как и до перехода на схему
	_ struct{} `additionalProperties:"true"`
	// границы логина меряет Resolve, схеме видна строка до нормализации
	Login    string `json:"login" doc:"Логин пользователя"`
	Password string `json:"password" minLength:"1" maxLength:"128" doc:"Пароль пользователя"`
}

// Resolve нормализует логин и меряет границы уже приведённого значения.
// Схема видит сырую строку, а хранилищу и лимитеру нужна приведённая.
func (b *registerBody) Resolve(huma.Context) []error {
	b.Login = auth.NormalizeLogin(b.Login)

	return validateLogin(b.Login)
}

// loginBody не применяет парольную политику, вход требует лишь непустых полей.
type loginBody struct {
	_        struct{} `additionalProperties:"true"`
	Login    string   `json:"login" doc:"Логин пользователя"`
	Password string   `json:"password" minLength:"1" doc:"Пароль пользователя"`
}

func (b *loginBody) Resolve(huma.Context) []error {
	b.Login = auth.NormalizeLogin(b.Login)

	return validateLogin(b.Login)
}

// loginError совмещает статус ответа и деталь с перечнем полей.
// huma берёт статус из ошибки резолвера, а перечень из её детали.
type loginError struct {
	detail *huma.ErrorDetail
}

func (e loginError) Error() string { return e.detail.Message }

func (e loginError) ErrorDetail() *huma.ErrorDetail { return e.detail }

func (e loginError) GetStatus() int { return http.StatusBadRequest }

// validateLogin ловит логин, пустой после обрезки пробелов.
// Схема его пропускает, а учётной записи без имени быть не должно.
func validateLogin(login string) []error {
	var message string
	switch {
	case login == "":
		message = MessageEmptyLogin
	case utf8.RuneCountInString(login) > auth.MaxLoginRunes:
		message = MessageLongLogin
	case strings.ContainsRune(login, '\x00'):
		message = MessageNullCharacter
	default:
		return nil
	}

	return []error{loginError{detail: &huma.ErrorDetail{
		Location: "body.login",
		Message:  message,
	}}}
}

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

// specStatusPaths собирает пути, для которых ТЗ задаёт перечень статусов.
var specStatusPaths sync.Map

// useSpecStatuses понижает 422 до 400 только на путях этого пакета.
// ТЗ допускает для аутентификации лишь 200, 400, 401, 409 и 500,
// а другим маршрутам 422 положен, например номеру заказа неверного формата.
var useSpecStatuses = sync.OnceFunc(func() {
	next := huma.NewErrorWithContext
	huma.NewErrorWithContext = func(
		ctx huma.Context,
		status int,
		msg string,
		errs ...error,
	) huma.StatusError {
		if status == http.StatusUnprocessableEntity && ctx != nil {
			if operation := ctx.Operation(); operation != nil {
				if _, ok := specStatusPaths.Load(operation.Path); ok {
					status = http.StatusBadRequest
				}
			}
		}

		return next(ctx, status, msg, errs...)
	}
})

// APIConfig описывает API без служебных маршрутов фреймворка.
// Набор публичных путей задан ТЗ, схемы и документация в него не входят.
func APIConfig() huma.Config {
	config := huma.DefaultConfig("Gophermart", "1.0.0")
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	// хук добавляет ссылку на описание схемы в тело и заголовок Link
	config.CreateHooks = nil
	config.Transformers = nil

	return config
}

// RegisterRoutes публикует маршруты аутентификации как huma-операции.
func RegisterRoutes(api huma.API, logger *zap.Logger, deps Deps) {
	useSpecStatuses()
	specStatusPaths.Store(registerPath, struct{}{})
	specStatusPaths.Store(loginPath, struct{}{})

	huma.Register(api, huma.Operation{
		OperationID: "register-user",
		Method:      http.MethodPost,
		Path:        registerPath,
		Summary:     "Регистрация пользователя",
		// пустое тело успеха не должно превращаться в 204
		DefaultStatus: http.StatusOK,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusConflict,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, in *registerInput) (*authOutput, error) {
		credentials := auth.NormalizeCredentials(in.Body.Login, in.Body.Password)
		token, err := deps.Register(ctx, credentials)
		switch {
		case err == nil:
			return authenticated(token), nil
		case errors.Is(err, auth.ErrLoginTaken):
			return nil, huma.Error409Conflict(MessageLoginTaken)
		default:
			logger.Error("Не удалось зарегистрировать пользователя", zap.Error(err))
			return nil, huma.Error500InternalServerError(MessageInternalError)
		}
	})

	huma.Register(api, huma.Operation{
		OperationID:   "login-user",
		Method:        http.MethodPost,
		Path:          loginPath,
		Summary:       "Вход пользователя",
		DefaultStatus: http.StatusOK,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, in *loginInput) (*authOutput, error) {
		credentials := auth.NormalizeCredentials(in.Body.Login, in.Body.Password)
		token, err := deps.Login(ctx, credentials)
		switch {
		case err == nil:
			return authenticated(token), nil
		case errors.Is(err, auth.ErrInvalidCredentials):
			return nil, huma.Error401Unauthorized(MessageInvalidCredentials)
		default:
			logger.Error("Не удалось выполнить вход пользователя", zap.Error(err))
			return nil, huma.Error500InternalServerError(MessageInternalError)
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
