package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func TestNew(t *testing.T) {
	s := mustNew(t, zap.NewNop(), config.Default())

	assert.Equal(t, config.DefaultRunAddress, s.runAddress)
	assert.Equal(t, DefaultShutdownTimeout, s.shutdownTimeout)
	assert.Equal(t, config.DefaultRequestBodyLimit, s.requestBodyLimit)
	assert.NotNil(t, s.router)
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
