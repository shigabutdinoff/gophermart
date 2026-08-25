package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

const testAccrualAddress = "localhost:8081"

func newTestConfig() config.Config {
	cfg := config.Default()
	cfg.JWTSecret = testJWTSecret
	cfg.AccrualAddress = testAccrualAddress

	return cfg
}

func mustNew(t *testing.T, logger *zap.Logger, cfg config.Config) *Server {
	t.Helper()
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = testJWTSecret
	}
	if cfg.AccrualAddress == "" {
		cfg.AccrualAddress = testAccrualAddress
	}
	server, err := New(logger, cfg)
	require.NoError(t, err)
	return server
}
