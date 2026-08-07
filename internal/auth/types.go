package auth

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidCredentialsFormat indicates credentials of an invalid format
	ErrInvalidCredentialsFormat = errors.New("invalid credentials format")
	// ErrLoginTaken indicates that a login is already registered.
	ErrLoginTaken = errors.New("login is already taken")
	// ErrUserNotFound indicates that no user exists for a lookup.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials indicates failed authentication of any cause.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrPasswordMismatch indicates that a password does not match a valid hash.
	ErrPasswordMismatch = errors.New("password mismatch")
)

type Clock func() time.Time

// Credentials хранит логин и пароль в открытом виде.
type Credentials struct {
	Login    string
	Password string
}

type User struct {
	ID           int64
	Login        string
	PasswordHash string
	CreatedAt    time.Time
}

type IssuedToken struct {
	Value     string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// UserRepository stores users and looks them up by normalized login.
type UserRepository interface {
	// Create persists a user with a password hash.
	Create(ctx context.Context, login, passwordHash string) (User, error)
	// FindByLogin returns the user with the supplied normalized login.
	FindByLogin(ctx context.Context, login string) (User, error)
}

// Passwords hashes cleartext passwords and verifies them against stored hashes.
type Passwords interface {
	// Hash returns a secure encoded hash of password.
	Hash(password string) (string, error)
	// Verify returns ErrPasswordMismatch only for a non-matching password.
	// Malformed hashes are returned as internal errors.
	Verify(passwordHash, password string) error
}

type TokenIssuer interface {
	Issue(userID int64) (IssuedToken, error)
}
