package authentication

import (
	"fmt"
	"net/http"

	"github.com/go-chi/render"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
)

// Тексты ответов маршрутов аутентификации.
const (
	MessageRegistered         = "Регистрация выполнена"
	MessageLoggedIn           = "Вход выполнен"
	MessageBadRequest         = "Переданы некорректные данные"
	MessageBodyTooLarge       = "Тело запроса слишком велико"
	MessageLoginTaken         = "Логин уже занят"
	MessageInvalidCredentials = "Неверная пара логин/пароль"
	MessageTooManyAttempts    = "Слишком много попыток аутентификации"
	MessageInternalError      = "Внутренняя ошибка сервиса"
)

// credentialsMessages переводит нарушенное правило формата в текст ответа.
// Границы берутся из auth, иначе смена лимита оставит врущее сообщение.
var credentialsMessages = map[auth.CredentialsFormatError]string{
	{Field: "login", Rule: "min"}: "Логин не может быть пустым",
	{Field: "login", Rule: "max"}: fmt.Sprintf(
		"Логин длиннее %d символов", auth.MaxLoginRunes,
	),
	{Field: "login", Rule: "nonul"}: "Логин содержит недопустимый символ",
	{Field: "password", Rule: "min"}: fmt.Sprintf(
		"Пароль короче %d символов", auth.MinPasswordRunes,
	),
	{Field: "password", Rule: "max"}: fmt.Sprintf(
		"Пароль длиннее %d символов", auth.MaxPasswordRunes,
	),
	{Field: "password", Rule: "required"}: "Пароль не может быть пустым",
}

// TooManyAttempts answers a login request rejected by the rate limit.
func TooManyAttempts(w http.ResponseWriter, r *http.Request) {
	writeMessage(w, r, http.StatusTooManyRequests, MessageTooManyAttempts)
}

// messageResponse is the single-field body of every authentication response.
type messageResponse struct {
	Message string `json:"message"`
}

// apiError переносит несостоявшийся ответ от разбора запроса к хендлеру.
type apiError struct {
	status  int
	message string
}

func badRequest(message string) *apiError {
	return &apiError{status: http.StatusBadRequest, message: message}
}

func writeError(w http.ResponseWriter, r *http.Request, apiErr *apiError) {
	writeMessage(w, r, apiErr.status, apiErr.message)
}

func writeMessage(w http.ResponseWriter, r *http.Request, status int, message string) {
	render.Status(r, status)
	render.JSON(w, r, messageResponse{Message: message})
}

func writeAuthenticated(
	w http.ResponseWriter,
	r *http.Request,
	now auth.Clock,
	token auth.IssuedToken,
	message string,
) {
	w.Header().Set("Authorization", "Bearer "+token.Value)
	http.SetCookie(w, &http.Cookie{
		Name:    authorization.SessionCookieName,
		Value:   token.Value,
		Path:    "/",
		Expires: token.ExpiresAt,
		// срок куки считается теми же часами, что выдали токен
		MaxAge:   int(token.ExpiresAt.Sub(now()).Seconds()),
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	writeMessage(w, r, http.StatusOK, message)
}
