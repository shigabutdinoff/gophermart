package server

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func successfulTestMigration(context.Context, *sql.DB) error {
	return nil
}

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func receiveWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		require.FailNow(t, "канал не ответил вовремя")
		var zero T

		return zero
	}
}

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
