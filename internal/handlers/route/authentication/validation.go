package authentication

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

// Тексты отказов по логину.
const (
	MessageNullCharacter = "Логин содержит недопустимый символ"
	MessageEmptyLogin    = "Логин не может быть пустым"
	MessageLongLogin     = "Логин слишком длинный"
)

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
