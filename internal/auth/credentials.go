package auth

import "strings"

// MaxLoginRunes ограничивает длину нормализованного логина.
const MaxLoginRunes = 255

// NormalizeLogin приводит логин к нижнему регистру и срезает крайние пробелы.
func NormalizeLogin(login string) string {
	return strings.ToLower(strings.TrimSpace(login))
}

// NormalizeCredentials нормализует логин и не трогает пароль.
// Границы значений проверяет схема маршрута, поэтому здесь их нет.
func NormalizeCredentials(login, password string) Credentials {
	return Credentials{
		Login:    NormalizeLogin(login),
		Password: password,
	}
}
