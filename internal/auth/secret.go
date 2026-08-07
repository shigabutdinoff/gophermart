package auth

import (
	"crypto/rand"
	"fmt"
)

// SecretSize is the size in bytes of a generated or configured secret
const SecretSize = 32

// ResolveSecret returns a copy of configured or generates a new process secret.
func ResolveSecret(configured string) ([]byte, error) {
	if configured != "" {
		if len(configured) < SecretSize {
			return nil, fmt.Errorf("configured secret must be at least %d bytes", SecretSize)
		}

		return []byte(configured), nil
	}

	secret := make([]byte, SecretSize)
	// crypto/rand паникует сам, вернуть ошибку здесь невозможно.
	_, _ = rand.Read(secret)

	return secret, nil
}
