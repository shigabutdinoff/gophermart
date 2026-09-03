package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLister struct {
	list   []Order
	err    error
	userID int64
}

func (l *fakeLister) ListByUser(_ context.Context, userID int64) ([]Order, error) {
	l.userID = userID

	return l.list, l.err
}

func TestListReturnsOrdersOfUser(t *testing.T) {
	uploaded := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	lister := &fakeLister{list: []Order{{Number: "12345678903", Status: StatusNew, UploadedAt: uploaded}}}

	list, err := NewListService(lister).List(context.Background(), 42)

	require.NoError(t, err)
	assert.Equal(t, int64(42), lister.userID)
	require.Len(t, list, 1)
	assert.Equal(t, "12345678903", list[0].Number)
	assert.Equal(t, StatusNew, list[0].Status)
	assert.Equal(t, uploaded, list[0].UploadedAt)
}

func TestListWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")

	_, err := NewListService(&fakeLister{err: storageErr}).List(context.Background(), 1)

	require.ErrorIs(t, err, storageErr)
}
