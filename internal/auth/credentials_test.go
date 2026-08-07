package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRegisterCredentials(t *testing.T) {
	t.Run("normalizes login and preserves password", func(t *testing.T) {
		input := Credentials{
			Login:    " \tЮЗЕР\u2003 ",
			Password: " Pass Word ",
		}

		got, err := NormalizeRegisterCredentials(input.Login, input.Password)
		require.NoError(t, err)

		assert.Equal(t, "юзер", got.Login)
		assert.Equal(t, input.Password, got.Password)
	})

	validTests := []struct {
		name  string
		input Credentials
	}{
		{
			name: "one login rune",
			input: Credentials{
				Login:    "я",
				Password: "password",
			},
		},
		{
			name: "255 login runes",
			input: Credentials{
				Login:    strings.Repeat("я", 255),
				Password: "password",
			},
		},
		{
			name: "eight password runes",
			input: Credentials{
				Login:    "user",
				Password: strings.Repeat("я", 8),
			},
		},
		{
			name: "128 password runes",
			input: Credentials{
				Login:    "user",
				Password: strings.Repeat("я", 128),
			},
		},
	}

	for _, tt := range validTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeRegisterCredentials(tt.input.Login, tt.input.Password)
			require.NoError(t, err)
			assert.Equal(t, tt.input.Password, got.Password)
		})
	}

}

func TestNormalizeRegisterCredentialsReportsViolatedRule(t *testing.T) {
	tests := []struct {
		name     string
		login    string
		password string
		field    string
		rule     string
	}{
		{"пустой login", "  ", "password", "login", "min"},
		{"login из пробелов Unicode", " \t\u2003\n", "password", "login", "min"},
		{"длинный login", strings.Repeat("я", 256), "password", "login", "max"},
		{"login с NUL", "user\x00suffix", "password", "login", "nonul"},
		{"короткий пароль", "user", strings.Repeat("p", 7), "password", "min"},
		{"длинный пароль", "user", strings.Repeat("p", 129), "password", "max"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeRegisterCredentials(tt.login, tt.password)

			require.ErrorIs(t, err, ErrInvalidCredentialsFormat)
			var format *CredentialsFormatError
			require.ErrorAs(t, err, &format)
			assert.Equal(t, tt.field, format.Field)
			assert.Equal(t, tt.rule, format.Rule)
		})
	}
}

func TestNormalizeRegisterCredentialsAcceptsLongASCIIPassword(t *testing.T) {
	got, err := NormalizeRegisterCredentials("user", strings.Repeat("p", 100))

	require.NoError(t, err)
	assert.Len(t, got.Password, 100)
}

func TestNormalizeLoginCredentialsSkipsPasswordPolicy(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{"короткий пароль", strings.Repeat("p", 7)},
		{"один символ", "p"},
		{"пароль длиннее 128 символов", strings.Repeat("p", 129)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeLoginCredentials(" ЮЗЕР ", tt.password)

			require.NoError(t, err)
			assert.Equal(t, "юзер", got.Login)
			assert.Equal(t, tt.password, got.Password)
		})
	}
}

func TestNormalizeLoginCredentialsReportsViolatedRule(t *testing.T) {
	tests := []struct {
		name     string
		login    string
		password string
		field    string
		rule     string
	}{
		{"пустой login", "  ", "password", "login", "min"},
		{"длинный login", strings.Repeat("я", 256), "password", "login", "max"},
		{"login с NUL", "user\x00suffix", "password", "login", "nonul"},
		{"пустой пароль", "user", "", "password", "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeLoginCredentials(tt.login, tt.password)

			require.ErrorIs(t, err, ErrInvalidCredentialsFormat)
			var format *CredentialsFormatError
			require.ErrorAs(t, err, &format)
			assert.Equal(t, tt.field, format.Field)
			assert.Equal(t, tt.rule, format.Rule)
		})
	}
}
