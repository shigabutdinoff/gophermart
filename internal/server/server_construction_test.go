package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/auth"
	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func TestNew_SignsWithKeyStoredAtStartup(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)

	server, err := New(zap.NewNop(), config.Default())

	require.NoError(t, err)
	t.Cleanup(server.closeDatabase)
	values, err := godotenv.Read(filepath.Join(directory, auth.SecretEnvFile))
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{64}$`, values[auth.SecretEnvName])
}

func TestNew_FailsWhenSigningKeyCannotBeStored(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir(auth.SecretEnvFile, 0o700))

	_, err := New(zap.NewNop(), config.Default())

	require.ErrorContains(t, err, "save signing key")
}

func TestNew(t *testing.T) {
	s := mustNew(t, zap.NewNop(), config.Default())

	assert.Equal(t, config.DefaultRunAddress, s.runAddress)
	assert.Equal(t, DefaultShutdownTimeout, s.shutdownTimeout)
	assert.Equal(t, config.DefaultRequestBodyLimit, s.requestBodyLimit)
	assert.NotNil(t, s.router)
}

func TestNew_ShutdownTimeoutOptionReplacesDefaultBudget(t *testing.T) {
	timeout := 3 * time.Second

	s := mustNew(t, zap.NewNop(), config.Default(), WithShutdownTimeout(timeout))

	assert.Equal(t, timeout, s.shutdownTimeout)
}

func TestNew_RejectsShortSecretBeforeListenWithoutLeak(t *testing.T) {
	cfg := config.Default()
	cfg.JWTSecret = "short-secret"

	server, err := New(zap.NewNop(), cfg)

	assert.Nil(t, server)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), cfg.JWTSecret)
}

func TestNew_BuildsAuthDependenciesBeforeRouter(t *testing.T) {
	cfg := config.Default()
	cfg.JWTSecret = "0123456789abcdef0123456789abcdef"

	server, err := New(zap.NewNop(), cfg)

	require.NoError(t, err)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.deps.auth.Register)
	assert.NotNil(t, server.deps.auth.Login)
	assert.NotNil(t, server.deps.tokenParser)
}

func TestNew_BuildsBalanceDependencies(t *testing.T) {
	server := mustNew(t, zap.NewNop(), config.Default())

	assert.NotNil(t, server.deps.balance.Read)
	assert.NotNil(t, server.deps.balance.Withdraw)
	assert.NotNil(t, server.deps.balance.List)
}

func TestServer_AddrBeforeListen(t *testing.T) {
	s := mustNew(t, zap.NewNop(), config.Default())
	assert.Empty(t, s.Addr())
}
