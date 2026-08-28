package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCloseDatabaseWithinReturnsCloseError(t *testing.T) {
	want := errors.New("close")
	err := closeDatabaseWithin(context.Background(), zap.NewNop(), func() error { return want })
	require.ErrorIs(t, err, want)
}

func TestCloseDatabaseWithinHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := closeDatabaseWithin(ctx, zap.NewNop(), func() error {
		<-ctx.Done()
		return nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
