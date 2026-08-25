package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

// testAccrualAddress убирает из журнала предупреждение об отключённом опросе
const testAccrualAddress = "localhost:8081"

func newTestConfig() config.Config {
	cfg := config.Default()
	cfg.JWTSecret = testJWTSecret
	cfg.AccrualAddress = testAccrualAddress

	return cfg
}

func mustNew(t *testing.T, logger *zap.Logger, cfg config.Config, options ...Option) *Server {
	t.Helper()
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = testJWTSecret
	}
	if cfg.AccrualAddress == "" {
		cfg.AccrualAddress = testAccrualAddress
	}
	server, err := New(logger, cfg, options...)
	require.NoError(t, err)
	return server
}

// notify отправляет сигнал, не блокируя отправителя на полном канале.
func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// receiveWithin ждёт события на реальных часах, вне synctest-бабла.
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
