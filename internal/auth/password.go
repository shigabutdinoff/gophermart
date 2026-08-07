package auth

import (
	"fmt"

	"github.com/alexedwards/argon2id"
)

// Профиль OWASP для argon2id, 19 MiB памяти и две итерации в один поток
var argon2Params = &argon2id.Params{
	Memory:      19 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

type Argon2Passwords struct{}

// Hash возвращает PHC-строку, соль у каждого хеша своя.
func (Argon2Passwords) Hash(password string) (string, error) {
	hash, err := argon2id.CreateHash(password, argon2Params)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return hash, nil
}

// Verify отвечает ErrPasswordMismatch только на несовпадение пароля,
// битый хеш возвращается как внутренняя ошибка.
func (Argon2Passwords) Verify(passwordHash, password string) error {
	match, err := argon2id.ComparePasswordAndHash(password, passwordHash)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !match {
		return ErrPasswordMismatch
	}
	return nil
}
