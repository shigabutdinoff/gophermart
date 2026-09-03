package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgon2PasswordsHashAndVerify(t *testing.T) {
	passwords := Argon2Passwords{}
	const cleartext = "same-password"

	firstHash, err := passwords.Hash(cleartext)
	require.NoError(t, err)
	secondHash, err := passwords.Hash(cleartext)
	require.NoError(t, err)

	assert.NotEqual(t, cleartext, firstHash)
	assert.NotEqual(t, firstHash, secondHash, "соль обязана быть своей на каждый хеш")

	assert.NoError(t, passwords.Verify(firstHash, cleartext))
	assert.NoError(t, passwords.Verify(secondHash, cleartext))
	assert.ErrorIs(t, passwords.Verify(firstHash, "wrong-password"), ErrPasswordMismatch)
}

func TestArgon2PasswordsEncodesParameters(t *testing.T) {
	hash, err := Argon2Passwords{}.Hash("password")
	require.NoError(t, err)

	assert.True(
		t,
		strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$"),
		"неожиданный формат: %s",
		hash,
	)
	assert.Len(t, strings.Split(hash, "$"), 6)
}

func TestArgon2PasswordsUsesWholeLongPassword(t *testing.T) {
	passwords := Argon2Passwords{}
	long := strings.Repeat("p", 100)

	hash, err := passwords.Hash(long)
	require.NoError(t, err)

	assert.NoError(t, passwords.Verify(hash, long))
	assert.ErrorIs(t, passwords.Verify(hash, long[:72]), ErrPasswordMismatch)
}

func TestArgon2PasswordsRejectsUnknownHashFormat(t *testing.T) {
	passwords := Argon2Passwords{}
	tests := []struct {
		name string
		hash string
	}{
		{"пустая строка", ""},
		{"мусор", "damaged"},
		{"хеш bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"чужой алгоритм", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaA"},
		{"чужая версия", "$argon2id$v=16$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaA"},
		{"неразборчивые параметры", "$argon2id$v=19$m=abc,t=2,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaA"},
		{"неразборчивая соль", "$argon2id$v=19$m=19456,t=2,p=1$!!!$aGFzaA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := passwords.Verify(tt.hash, "password")

			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrPasswordMismatch, "повреждённый хеш обязан дать 500")
		})
	}
}
