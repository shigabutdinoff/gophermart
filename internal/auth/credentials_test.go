package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeCredentials(t *testing.T) {
	tests := []struct {
		name     string
		login    string
		password string
		want     string
	}{
		{"крайние пробелы и регистр", " \tЮЗЕР  ", " Pass Word ", "юзер"},
		{"уже нормализованный логин", "user", "password", "user"},
		{"один символ", "я", "password", "я"},
		{"255 символов", strings.Repeat("я", 255), "password", strings.Repeat("я", 255)},
		{"пустой логин остаётся пустым", "   ", "password", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeCredentials(tt.login, tt.password)

			assert.Equal(t, tt.want, got.Login)
			assert.Equal(t, tt.password, got.Password, "пароль не нормализуется")
		})
	}
}
