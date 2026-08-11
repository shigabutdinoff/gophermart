package gophermart

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		"RUN_ADDRESS",
		"DATABASE_URI",
		"ACCRUAL_SYSTEM_ADDRESS",
		"REQUEST_BODY_LIMIT",
		"JWT_SECRET",
		"QUEUE_NAME",
		"QUEUE_WORKERS",
		"QUEUE_FETCH_POLL_INTERVAL",
		"QUEUE_ACCRUAL_POLL_INTERVAL",
		"QUEUE_THROTTLE_BACKOFF",
		"QUEUE_MAX_ATTEMPTS",
	} {
		// t.Setenv запоминает исходное состояние, Unsetenv очищает на время теста
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

// withEnvFile переносит тест в свой каталог с env-файлом заданного содержимого
func withEnvFile(t *testing.T, content string) {
	t.Helper()

	clearEnv(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(".env", []byte(content), 0o600))
}

func TestParse_JWTSecretComesOnlyFromEnvironment(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	clearEnv(t)
	t.Setenv("JWT_SECRET", secret)

	cfg, err := Parse(nil)

	require.NoError(t, err)
	assert.Equal(t, secret, cfg.JWTSecret)

	_, err = Parse([]string{"--jwt-secret", secret})
	require.Error(t, err)
}

func TestParse_EnvFileFillsOnlyMissingVariables(t *testing.T) {
	const fileSecret = "file-secret-0123456789abcdef0123"
	withEnvFile(t, "JWT_SECRET="+fileSecret+"\nDATABASE_URI=file-db\n")
	t.Setenv("DATABASE_URI", "env-db")

	cfg, err := Parse(nil)

	require.NoError(t, err)
	assert.Equal(t, fileSecret, cfg.JWTSecret)
	assert.Equal(t, "env-db", cfg.DatabaseURI, "переменная окружения важнее .env")
}

func TestParse_EmptyProcessVariableDoesNotShadowEnvFile(t *testing.T) {
	const fileSecret = "file-secret-0123456789abcdef0123"
	withEnvFile(t, "JWT_SECRET="+fileSecret+"\nRUN_ADDRESS=file:1\n")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("RUN_ADDRESS", "")

	cfg, err := Parse(nil)

	require.NoError(t, err)
	assert.Equal(t, fileSecret, cfg.JWTSecret, "явно пустая переменная не отменяет ключ из файла")
	assert.Equal(t, "file:1", cfg.RunAddress, "правило одинаково для всех параметров")
}

func TestParse_ExplicitFlagBeatsEnvFile(t *testing.T) {
	withEnvFile(t, "RUN_ADDRESS=file:1\n")

	cfg, err := Parse([]string{"-a", "flag:2"})

	require.NoError(t, err)
	assert.Equal(t, "flag:2", cfg.RunAddress)
}

func TestParse_BrokenEnvFileIsReported(t *testing.T) {
	withEnvFile(t, "JWT_SECRET\n")

	_, err := Parse(nil)

	require.ErrorContains(t, err, "load env file")
}

func TestParse_Defaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Parse(nil)
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
}

func TestParse_EnvironmentOverridesDefaults(t *testing.T) {
	t.Setenv("RUN_ADDRESS", "env:1")
	t.Setenv("DATABASE_URI", "env-db")
	t.Setenv("ACCRUAL_SYSTEM_ADDRESS", "env-accrual")
	t.Setenv("REQUEST_BODY_LIMIT", "2048")
	cfg, err := Parse(nil)
	require.NoError(t, err)
	assert.Equal(t, "env:1", cfg.RunAddress)
	assert.Equal(t, "env-db", cfg.DatabaseURI)
	assert.Equal(t, "env-accrual", cfg.AccrualAddress)
	assert.Equal(t, int64(2048), cfg.RequestBodyLimit)
}

func TestParse_ExplicitFlagsOverrideEnvironment(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"короткие флаги", []string{"-a", "flag:2", "-d", "flag-db", "-r", "flag-accrual", "-l", "4096"}},
		{"длинные флаги", []string{
			"--run-address", "flag:2",
			"--database-uri", "flag-db",
			"--accrual-system-address", "flag-accrual",
			"--request-body-limit", "4096",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RUN_ADDRESS", "env:1")
			t.Setenv("DATABASE_URI", "env-db")
			t.Setenv("ACCRUAL_SYSTEM_ADDRESS", "env-accrual")
			t.Setenv("REQUEST_BODY_LIMIT", "2048")
			cfg, err := Parse(tc.args)
			require.NoError(t, err)
			assert.Equal(t, "flag:2", cfg.RunAddress)
			assert.Equal(t, "flag-db", cfg.DatabaseURI)
			assert.Equal(t, "flag-accrual", cfg.AccrualAddress)
			assert.Equal(t, int64(4096), cfg.RequestBodyLimit)
		})
	}
}

func TestParse_LongFlagAliases(t *testing.T) {
	clearEnv(t)
	cfg, err := Parse([]string{
		"--run-address", "long:1",
		"--database-uri", "long-db",
		"--accrual-system-address", "long-accrual",
		"--request-body-limit", "4096",
	})
	require.NoError(t, err)
	assert.Equal(t, "long:1", cfg.RunAddress)
	assert.Equal(t, "long-db", cfg.DatabaseURI)
	assert.Equal(t, "long-accrual", cfg.AccrualAddress)
	assert.Equal(t, int64(4096), cfg.RequestBodyLimit)
}

func TestParse_RejectsNonPositiveLimit(t *testing.T) {
	_, err := Parse([]string{"-l", "0"})
	require.Error(t, err)
}

func TestParse_RejectsEmptyRunAddress(t *testing.T) {
	_, err := Parse([]string{"-a", ""})
	require.Error(t, err)
}

func TestParse_RejectsPositionalArguments(t *testing.T) {
	_, err := Parse([]string{"unexpected"})
	require.Error(t, err)
}

func TestParse_RejectsInvalidLimitEnv(t *testing.T) {
	t.Setenv("REQUEST_BODY_LIMIT", "not-a-number")
	_, err := Parse(nil)
	require.Error(t, err)
}

func TestParse_FlagErrorNotPrintedToStderr(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })

	_, parseErr := Parse([]string{"-unknown"})

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)

	require.Error(t, parseErr)
	assert.Empty(t, string(out))
}

func TestParse_HelpPrintsUsage(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })

	_, parseErr := Parse([]string{"-h"})

	require.NoError(t, w.Close())
	usage, err := io.ReadAll(r)
	require.NoError(t, err)

	require.ErrorIs(t, parseErr, ErrHelp)
	assert.Contains(t, string(usage), "run-address")
	assert.Contains(t, string(usage), "request-body-limit")
}

// Обе формы каждого флага доступны и попадают в своё поле конфигурации.
func TestParse_ShortAndLongFormsFillSameFields(t *testing.T) {
	short, err := Parse([]string{"-a", "short:1", "-d", "dsn-short", "-r", "accrual-short", "-l", "2048"})
	require.NoError(t, err)

	long, err := Parse([]string{
		"--run-address", "short:1",
		"--database-uri", "dsn-short",
		"--accrual-system-address", "accrual-short",
		"--request-body-limit", "2048",
	})
	require.NoError(t, err)

	assert.Equal(t, short, long)
	assert.Equal(t, "short:1", short.RunAddress)
	assert.Equal(t, "dsn-short", short.DatabaseURI)
	assert.Equal(t, "accrual-short", short.AccrualAddress)
	assert.EqualValues(t, 2048, short.RequestBodyLimit)
}

func TestParse_QueueKeepsDefaultsWithoutEnvironment(t *testing.T) {
	clearEnv(t)

	cfg, err := Parse(nil)

	require.NoError(t, err)
	assert.Equal(t, Default().Queue, cfg.Queue)
}

func TestParse_QueueComesFromEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("QUEUE_NAME", "orders")
	t.Setenv("QUEUE_WORKERS", "8")
	t.Setenv("QUEUE_FETCH_POLL_INTERVAL", "250ms")
	t.Setenv("QUEUE_ACCRUAL_POLL_INTERVAL", "3s")
	t.Setenv("QUEUE_THROTTLE_BACKOFF", "30s")
	t.Setenv("QUEUE_MAX_ATTEMPTS", "3")

	cfg, err := Parse(nil)

	require.NoError(t, err)
	assert.Equal(t, QueueConfig{
		Name:                "orders",
		Workers:             8,
		FetchPollInterval:   250 * time.Millisecond,
		AccrualPollInterval: 3 * time.Second,
		ThrottleBackoff:     30 * time.Second,
		MaxAttempts:         3,
	}, cfg.Queue)
}

func TestParse_RejectsUnusableQueueSettings(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "QUEUE_NAME", value: "   "},
		{name: "QUEUE_NAME", value: "Orders"},
		{name: "QUEUE_NAME", value: "order queue"},
		{name: "QUEUE_NAME", value: "-orders"},
		{name: "QUEUE_WORKERS", value: "0"},
		{name: "QUEUE_WORKERS", value: "10001"},
		{name: "QUEUE_FETCH_POLL_INTERVAL", value: "99ms"},
		{name: "QUEUE_ACCRUAL_POLL_INTERVAL", value: "0s"},
		{name: "QUEUE_THROTTLE_BACKOFF", value: "0s"},
		{name: "QUEUE_MAX_ATTEMPTS", value: "0"},
	}

	for _, test := range tests {
		t.Run(test.name+"="+test.value, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(test.name, test.value)

			_, err := Parse(nil)

			require.Error(t, err)
		})
	}
}

func TestParse_TrimsQueueName(t *testing.T) {
	clearEnv(t)
	t.Setenv("QUEUE_NAME", " orders ")

	cfg, err := Parse(nil)

	require.NoError(t, err)
	assert.Equal(t, "orders", cfg.Queue.Name, "пробелы очередь в имени не примет")
}

func TestParse_QueueNameLengthBoundary(t *testing.T) {
	clearEnv(t)
	t.Setenv("QUEUE_NAME", strings.Repeat("a", 64))

	_, err := Parse(nil)
	require.NoError(t, err)

	t.Setenv("QUEUE_NAME", strings.Repeat("a", 65))
	_, err = Parse(nil)
	require.ErrorContains(t, err, "at most 64")
}
