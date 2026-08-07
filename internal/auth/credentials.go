package auth

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MinPasswordRunes is the shortest password accepted at registration.
	MinPasswordRunes = 8
	// MaxPasswordRunes is the longest password accepted at registration.
	MaxPasswordRunes = 128
	// MaxLoginRunes is the longest login accepted anywhere.
	MaxLoginRunes = 255
)

// CredentialsFormatError reports which credentials rule was violated.
type CredentialsFormatError struct {
	// Field is the lowercase name of the offending field.
	Field string
	// Rule is the name of the violated validation rule.
	Rule string
}

func (e *CredentialsFormatError) Error() string {
	return fmt.Sprintf("credentials field %s violates rule %s", e.Field, e.Rule)
}

// Unwrap matches every format violation with ErrInvalidCredentialsFormat
func (e *CredentialsFormatError) Unwrap() error {
	return ErrInvalidCredentialsFormat
}

// NormalizeRegisterCredentials applies the password policy of a new account
func NormalizeRegisterCredentials(login, password string) (Credentials, error) {
	normalized := normalizeCredentials(login, password)
	if err := validateLogin(normalized.Login); err != nil {
		return Credentials{}, err
	}

	passwordRunes := utf8.RuneCountInString(password)
	if passwordRunes < MinPasswordRunes {
		return Credentials{}, formatError("password", "min")
	}
	if passwordRunes > MaxPasswordRunes {
		return Credentials{}, formatError("password", "max")
	}

	return normalized, nil
}

// NormalizeLoginCredentials keeps a login attempt free of the password policy
func NormalizeLoginCredentials(login, password string) (Credentials, error) {
	normalized := normalizeCredentials(login, password)
	if err := validateLogin(normalized.Login); err != nil {
		return Credentials{}, err
	}
	if password == "" {
		return Credentials{}, formatError("password", "required")
	}

	return normalized, nil
}

// normalizeCredentials приводит логин к нижнему регистру и не трогает пароль
func normalizeCredentials(login, password string) Credentials {
	return Credentials{
		Login:    strings.ToLower(strings.TrimSpace(login)),
		Password: password,
	}
}

func validateLogin(login string) error {
	loginRunes := utf8.RuneCountInString(login)
	if loginRunes < 1 {
		return formatError("login", "min")
	}
	if loginRunes > MaxLoginRunes {
		return formatError("login", "max")
	}
	if strings.ContainsRune(login, '\x00') {
		return formatError("login", "nonul")
	}

	return nil
}

func formatError(field, rule string) error {
	return &CredentialsFormatError{
		Field: field,
		Rule:  rule,
	}
}
