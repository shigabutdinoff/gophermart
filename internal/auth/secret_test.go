package auth

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestResolveSecretUsesConfiguredValue(t *testing.T) {
	configured := strings.Repeat("s", 32) + "-configured"

	first, err := ResolveSecret(configured)
	require.NoError(t, err)
	require.Equal(t, configured, string(first))

	second, err := ResolveSecret(configured)
	require.NoError(t, err)
	first[0] = 'x'
	require.Equal(t, configured, string(second))
}

func TestResolveSecretRejectsShortConfiguredValueWithoutLeakingIt(t *testing.T) {
	const configured = "too-short-private-value"

	_, err := ResolveSecret(configured)
	require.Error(t, err)
	require.NotContains(t, err.Error(), configured)
}

func TestResolveSecretRejectsMissingValue(t *testing.T) {
	_, err := ResolveSecret("")

	require.Error(t, err)
}

func TestResolveSecretAcceptsGeneratedValue(t *testing.T) {
	secret, err := ResolveSecret(GenerateSecret())

	require.NoError(t, err)
	require.Len(t, secret, hex.EncodedLen(SecretSize))
}

func TestGenerateSecretIsHexOfSecretSize(t *testing.T) {
	secret := GenerateSecret()

	decoded, err := hex.DecodeString(secret)
	require.NoError(t, err)
	require.Len(t, decoded, SecretSize)
	require.NotEqual(t, secret, GenerateSecret())
}

// saveSecret скрывает возвращённый ключ там, где тест проверяет только файл.
func saveSecret(t *testing.T, path, secret string) {
	t.Helper()

	_, err := SaveSecret(path, secret)
	require.NoError(t, err)
}

// newEnvFile создаёт env-файл с правами владельца и возвращает путь к нему.
func newEnvFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// assertSecretStored проверяет, что ключ виден разбору и записан один раз.
func assertSecretStored(t *testing.T, existing string) {
	t.Helper()

	path := newEnvFile(t, existing)
	before, err := godotenv.Read(path)
	require.NoError(t, err)

	saveSecret(t, path, "generated-secret")

	values, err := godotenv.Read(path)
	require.NoError(t, err)
	assert.Equal(t, "generated-secret", values[SecretEnvName])
	for name, value := range before {
		if name == SecretEnvName {
			continue
		}
		assert.Equal(t, value, values[name], "переменная %s потеряна", name)
	}
	assert.Equal(t, 1, secretDefinitions(t, path))
}

// secretDefinitions считает строки файла, задающие ключ подписи.
func secretDefinitions(t *testing.T, path string) int {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	count := 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), SecretEnvName+"=") {
			count++
		}
	}
	return count
}

func TestSaveSecretWritesKeyOnce(t *testing.T) {
	path := newEnvFile(t, "DB_DATABASE=praktikum")

	saveSecret(t, path, "first-secret")
	saveSecret(t, path, "second-secret")

	values, err := godotenv.Read(path)
	require.NoError(t, err)
	assert.Equal(t, "first-secret", values[SecretEnvName])
	assert.Equal(t, "praktikum", values["DB_DATABASE"])
	assert.Equal(t, 1, secretDefinitions(t, path))
}

// Заданность ключа определяет godotenv, тем же разбором читает конфигурация.
func TestSaveSecretFollowsGodotenvView(t *testing.T) {
	tests := []struct {
		name     string
		existing string
	}{
		{"пустое значение", "JWT_SECRET=\nDB=praktikum\n"},
		{"двойные кавычки", "JWT_SECRET=\"\"\n"},
		{"одинарные кавычки", "JWT_SECRET=''\n"},
		{"пробелы после знака равенства", "JWT_SECRET=   \n"},
		{"экспорт", "export JWT_SECRET=\n"},
		{"отступ", "\tJWT_SECRET=\"\"\n"},
		{"значение внутри чужой строки", "FOO=\"a\nJWT_SECRET=stale\nb\"\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSecretStored(t, tt.existing)
		})
	}
}

// Побеждает последнее определение ключа, дозапись встаёт последней.
func TestSaveSecretOutweighsEveryEmptyDefinition(t *testing.T) {
	tests := []struct {
		name     string
		existing string
	}{
		{"два пустых определения", "JWT_SECRET=\nDB=praktikum\nJWT_SECRET=\n"},
		{"пустое определение после заполненного", "JWT_SECRET=old-secret\nJWT_SECRET=\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSecretStored(t, tt.existing)
		})
	}
}

// Ссылка на ключ стоит выше дописанной строки и раскрывается в пустоту.
func TestSaveSecretLeavesEarlierReferenceEmpty(t *testing.T) {
	path := newEnvFile(t, "JWT_SECRET=\nDERIVED=$JWT_SECRET\n")

	saveSecret(t, path, "generated-secret")

	values, err := godotenv.Read(path)
	require.NoError(t, err)
	assert.Equal(t, "generated-secret", values[SecretEnvName])
	assert.Empty(t, values["DERIVED"])
}

// Строка ключа внутри многострочного значения принадлежит чужой переменной.
func TestSaveSecretKeepsMultilineNeighbour(t *testing.T) {
	path := newEnvFile(t, "FOO=\"a\nJWT_SECRET=inner\nb\"\nJWT_SECRET=\n")

	saveSecret(t, path, "generated-secret")

	values, err := godotenv.Read(path)
	require.NoError(t, err)
	assert.Equal(t, "generated-secret", values[SecretEnvName])
	assert.Equal(t, "a\nJWT_SECRET=inner\nb", values["FOO"])
}

// Строка с комментарием после знака равенства задаёт ключ его текстом.
func TestSaveSecretKeepsCommentedValue(t *testing.T) {
	existing := "JWT_SECRET= # ключ появится позже\n"
	path := newEnvFile(t, existing)

	saveSecret(t, path, "generated-secret")

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, existing, string(content))
}

// Имя, начинающееся с export, не должно приниматься за экспорт ключа.
func TestSaveSecretIgnoresLookalikeNames(t *testing.T) {
	path := newEnvFile(t, "exportJWT_SECRET=\nOTHER_JWT_SECRET=\n")

	saveSecret(t, path, "generated-secret")

	values, err := godotenv.Read(path)
	require.NoError(t, err)
	assert.Equal(t, "generated-secret", values[SecretEnvName])
	assert.Contains(t, values, "exportJWT_SECRET")
	assert.Contains(t, values, "OTHER_JWT_SECRET")
}

// Ключ из файла возвращается вызывающему, иначе он подпишет токены своим.
func TestSaveSecretKeepsConfiguredKey(t *testing.T) {
	path := newEnvFile(t, "JWT_SECRET=configured\n")

	stored, err := SaveSecret(path, "generated-secret")

	require.NoError(t, err)
	assert.Equal(t, "configured", stored)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "JWT_SECRET=configured\n", string(content))
}

func TestSaveSecretReturnsWrittenKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")

	stored, err := SaveSecret(path, "generated-secret")

	require.NoError(t, err)
	assert.Equal(t, "generated-secret", stored)
}

// При отказе записи прежний файл остаётся целым, а не обрезается.
func TestSaveSecretKeepsFileOnWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	existing := "DB_DATABASE=praktikum\nJWT_SECRET=\nAPP_NAME=gophermart\n"
	require.NoError(t, os.WriteFile(path, []byte(existing), 0o400))

	_, err := SaveSecret(path, "generated-secret")

	require.Error(t, err)
	require.NotContains(t, err.Error(), "generated-secret")
	content, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, existing, string(content))
}

// Права существующего файла принадлежат владельцу и не переписываются.
func TestSaveSecretKeepsExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("JWT_SECRET=\n"), 0o640))

	saveSecret(t, path, "generated-secret")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

// Симлинк остаётся симлинком, общий файл не подменяется.
func TestSaveSecretWritesThroughSymlink(t *testing.T) {
	tests := []struct {
		name         string
		createTarget bool
		relative     bool
	}{
		{name: "цель существует", createTarget: true},
		{name: "цель ещё не создана"},
		{name: "относительная ссылка на несозданную цель", relative: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "shared.env")
			path := filepath.Join(dir, ".env")
			if tt.createTarget {
				require.NoError(t, os.WriteFile(target, []byte("JWT_SECRET=\n"), 0o600))
			}
			link := target
			if tt.relative {
				link = "shared.env"
			}
			require.NoError(t, os.Symlink(link, path))

			saveSecret(t, path, "generated-secret")

			info, err := os.Lstat(path)
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&os.ModeSymlink, "ссылка не должна подменяться файлом")
			values, err := godotenv.Read(target)
			require.NoError(t, err)
			assert.Equal(t, "generated-secret", values[SecretEnvName])
		})
	}
}

// Кольцо симлинков даёт ошибку, а не зависание.
func TestSaveSecretReportsSymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	other := filepath.Join(dir, ".env-other")
	require.NoError(t, os.Symlink(other, path))
	require.NoError(t, os.Symlink(path, other))

	_, err := SaveSecret(path, "generated-secret")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many levels of symbolic links")
	assert.NotContains(t, err.Error(), "generated-secret")
}

func TestSaveSecretReportsUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-directory", ".env")

	_, err := SaveSecret(path, "generated-secret")

	require.Error(t, err)
	require.NotContains(t, err.Error(), "generated-secret")
}

func TestEnsureSecretKeepsConfiguredValue(t *testing.T) {
	t.Chdir(t.TempDir())
	configured := strings.Repeat("c", 64)
	core, logs := observer.New(zap.WarnLevel)

	secret, err := EnsureSecret(zap.New(core), configured)

	require.NoError(t, err)
	assert.Equal(t, configured, secret)
	assert.NoFileExists(t, SecretEnvFile, "заданный ключ не пишется в env-файл")
	assert.Zero(t, logs.Len())
}

func TestEnsureSecretStoresGeneratedValue(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	core, logs := observer.New(zap.WarnLevel)

	secret, err := EnsureSecret(zap.New(core), "")

	require.NoError(t, err)
	values, err := godotenv.Read(filepath.Join(directory, SecretEnvFile))
	require.NoError(t, err)
	assert.Equal(t, secret, values[SecretEnvName])
	assert.Regexp(t, `^[0-9a-f]{64}$`, secret)
	assert.Equal(t, 1, logs.FilterMessage("Ключ подписи не задан, сгенерирован новый").Len())
}

// Ключ, появившийся в файле после чтения конфигурации, побеждает свежий.
func TestEnsureSecretAdoptsValueStoredMeanwhile(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	stored := strings.Repeat("e", 64)
	existing := "JWT_SECRET=" + stored + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, SecretEnvFile), []byte(existing), 0o600))
	core, logs := observer.New(zap.WarnLevel)

	secret, err := EnsureSecret(zap.New(core), "")

	require.NoError(t, err)
	assert.Equal(t, stored, secret)
	content, err := os.ReadFile(filepath.Join(directory, SecretEnvFile))
	require.NoError(t, err)
	assert.Equal(t, existing, string(content))
	assert.Equal(t, 1, logs.FilterMessage("Ключ подписи не задан, взят из env-файла").Len())
}

func TestEnsureSecretReportsUnwritableEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir(SecretEnvFile, 0o700))

	_, err := EnsureSecret(zap.NewNop(), "")

	require.ErrorContains(t, err, "save signing key")
}
