package auth

import (
	"errors"
	"time"
)

var (
	// ErrInvalidCredentialsFormat indicates credentials of an invalid format
	ErrInvalidCredentialsFormat = errors.New("invalid credentials format")
	// ErrPasswordMismatch indicates that a password does not match a valid hash.
	ErrPasswordMismatch = errors.New("password mismatch")
)

type Clock func() time.Time

// Credentials хранит логин и пароль в открытом виде.
type Credentials struct {
	Login    string
	Password string
}

type IssuedToken struct {
	Value     string
	IssuedAt  time.Time
	ExpiresAt time.Time
}
