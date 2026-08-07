package auth

import (
	"errors"
)

var (
	// ErrInvalidCredentialsFormat indicates credentials of an invalid format
	ErrInvalidCredentialsFormat = errors.New("invalid credentials format")
	// ErrPasswordMismatch indicates that a password does not match a valid hash.
	ErrPasswordMismatch = errors.New("password mismatch")
)

// Credentials хранит логин и пароль в открытом виде.
type Credentials struct {
	Login    string
	Password string
}
