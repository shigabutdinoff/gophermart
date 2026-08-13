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

type UserCreator interface {
	Create(ctx context.Context, login, passwordHash string) (User, error)
}

// UserFinder ищет пользователя по уже нормализованному логину.
type UserFinder interface {
	FindByLogin(ctx context.Context, login string) (User, error)
}

type PasswordHasher interface {
	Hash(password string) (string, error)
}

type PasswordVerifier interface {
	// несовпадение пароля даёт ErrPasswordMismatch,
	// битый хеш возвращается как внутренняя ошибка
	Verify(passwordHash, password string) error
}

type TokenIssuer interface {
	Issue(userID int64) (IssuedToken, error)
}
