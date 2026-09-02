package auth

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidCredentialsFormat означает учётные данные негодного формата.
	ErrInvalidCredentialsFormat = errors.New("invalid credentials format")
	// ErrLoginTaken означает, что логин уже занят.
	ErrLoginTaken = errors.New("login is already taken")
	// ErrUserNotFound означает, что пользователя с таким логином нет.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials скрывает причину неудачной аутентификации.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrPasswordMismatch означает, что пароль не подходит к сохранённому хешу.
	ErrPasswordMismatch = errors.New("password mismatch")
)

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
