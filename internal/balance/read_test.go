package balance

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shigabutdinoff/gophermart/internal/money"
)

type fakeReader struct {
	balance Balance
	err     error
	userID  int64
}

func (r *fakeReader) Balance(_ context.Context, userID int64) (Balance, error) {
	r.userID = userID

	return r.balance, r.err
}

func TestReadReturnsUserBalance(t *testing.T) {
	want := Balance{Current: money.Points(50050), Withdrawn: money.Points(4200)}
	reader := &fakeReader{balance: want}

	got, err := NewReadService(reader).Read(context.Background(), 42)

	require.NoError(t, err)
	assert.Equal(t, int64(42), reader.userID)
	assert.Equal(t, want, got)
}

func TestReadWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")

	_, err := NewReadService(&fakeReader{err: storageErr}).Read(context.Background(), 42)

	require.ErrorIs(t, err, storageErr)
	assert.ErrorContains(t, err, "read balance")
}
