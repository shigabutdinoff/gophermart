package gophermart

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{"RUN_ADDRESS", "DATABASE_URI", "ACCRUAL_SYSTEM_ADDRESS", "REQUEST_BODY_LIMIT"} {
		// t.Setenv запоминает исходное состояние, Unsetenv очищает на время теста
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
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

	require.ErrorIs(t, parseErr, flag.ErrHelp)
	assert.Contains(t, string(usage), "run-address")
	assert.Contains(t, string(usage), "request-body-limit")
}
