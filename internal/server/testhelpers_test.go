package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func mustNew(t *testing.T, logger *zap.Logger, cfg config.Config) *Server {
	t.Helper()
	server, err := New(logger, cfg)
	require.NoError(t, err)
	return server
}
