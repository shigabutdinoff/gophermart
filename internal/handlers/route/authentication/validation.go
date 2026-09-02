package authentication

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/shigabutdinoff/gophermart/internal/auth"
)

// Тексты отказов по логину и паролю.
const (
	MessageNullCharacter = "Логин содержит недопустимый символ"
	MessageEmptyLogin    = "Логин не может быть пустым"
	MessageLongLogin     = "Логин слишком длинный"
	MessageEmptyPassword = "Пароль не может быть пустым"
	MessageShortPassword = "Пароль слишком короткий"
	MessageLongPassword  = "Пароль слишком длинный"
)

// MinPasswordRunes держит нижнюю границу пароля при регистрации.
const MinPasswordRunes = 8

// MaxPasswordRunes ограничивает пароль до счёта argon2id: без границы в
// хеширование ушла бы строка размером во всё допустимое тело запроса.
const MaxPasswordRunes = 128

// fieldError совмещает статус ответа и деталь с перечнем полей.
// huma берёт статус из ошибки резолвера, а перечень из её детали.
// Значения поля деталь не несёт, поэтому пароль наружу не уходит.
type fieldError struct {
	detail *huma.ErrorDetail
}

func (e fieldError) Error() string { return e.detail.Message }

func (e fieldError) ErrorDetail() *huma.ErrorDetail { return e.detail }

func (e fieldError) GetStatus() int { return http.StatusBadRequest }

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

	return []error{fieldError{detail: &huma.ErrorDetail{
		Location: "body.login",
		Message:  message,
	}}}
}

// validatePassword держит те же границы, что раньше стояли тегами схемы.
func validatePassword(password string) []error {
	var message string
	switch {
	case password == "":
		message = MessageEmptyPassword
	case utf8.RuneCountInString(password) > MaxPasswordRunes:
		message = MessageLongPassword
	default:
		return nil
	}

	return []error{fieldError{detail: &huma.ErrorDetail{
		Location: "body.password",
		Message:  message,
	}}}
}

// validateNewPassword добавляет к общим границам нижнюю границу регистрации.
func validateNewPassword(password string) []error {
	if errs := validatePassword(password); errs != nil {
		return errs
	}
	if utf8.RuneCountInString(password) >= MinPasswordRunes {
		return nil
	}

	return []error{fieldError{detail: &huma.ErrorDetail{
		Location: "body.password",
		Message:  MessageShortPassword,
	}}}
}
