package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

// SecretSize задаёт, сколько случайных байт уходит в новый ключ.
const SecretSize = 32

// Ключ короче MinSecretLength для HS256 не принимается.
const MinSecretLength = 32

// SecretEnvName называет переменную окружения с ключом подписи.
const SecretEnvName = "JWT_SECRET"

// В SecretEnvFile дописывается сгенерированный ключ.
const SecretEnvFile = ".env"

// EnsureSecret берёт заданный ключ, иначе генерирует новый и сохраняет его.
func EnsureSecret(logger *zap.Logger, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	generated := GenerateSecret()
	stored, err := SaveSecret(SecretEnvFile, generated)
	if err != nil {
		return "", fmt.Errorf("save signing key: %w", err)
	}

	message := "Ключ подписи не задан, сгенерирован новый"
	if stored != generated {
		// файл успел получить ключ, подписывать нужно им
		message = "Ключ подписи не задан, взят из env-файла"
	}
	logger.Warn(message, zap.String("file", SecretEnvFile))

	return stored, nil
}

// ResolveSecret проверяет длину ключа и возвращает собственную копию.
// Ключ создаётся в одном месте, поэтому пустое значение сюда не приходит.
func ResolveSecret(configured string) ([]byte, error) {
	if err := checkSecretLength(len(configured)); err != nil {
		return nil, err
	}

	return []byte(configured), nil
}

// checkSecretLength меряет длину ключа одинаково для всех источников.
func checkSecretLength(length int) error {
	if length < MinSecretLength {
		return fmt.Errorf("signing key must be at least %d characters", MinSecretLength)
	}

	return nil
}

// GenerateSecret возвращает новый случайный ключ hex-строкой.
func GenerateSecret() string {
	secret := make([]byte, SecretSize)
	// crypto/rand паникует сам, вернуть ошибку здесь невозможно.
	_, _ = rand.Read(secret)

	return hex.EncodeToString(secret)
}

// SaveSecret кладёт ключ в env-файл, чтобы следующий запуск взял тот же.
// Файл пересобирается целиком, прежние переменные сохраняются.
func SaveSecret(path, secret string) (string, error) {
	values, err := godotenv.Read(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		values = make(map[string]string)
	}
	// чужой ключ появился между чтением конфигурации и записью, он и победит
	if stored := values[SecretEnvName]; stored != "" {
		return stored, nil
	}

	values[SecretEnvName] = secret
	if err := godotenv.Write(values, path); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	return secret, nil
}
