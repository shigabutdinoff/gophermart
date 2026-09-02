package auth

import (
	"crypto/rand"
	"fmt"
)

// SecretSize задаёт длину секрета в байтах
const SecretSize = 32

// ResolveSecret отдаёт копию заданного секрета либо заводит новый на процесс.
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
