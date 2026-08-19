package order

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStorage struct {
	outcome CreateOutcome
	err     error
	number  string
	userID  int64
	calls   int
}

func (s *fakeStorage) CreateOrFindOwner(
	_ context.Context,
	number string,
	userID int64,
) (CreateOutcome, error) {
	s.calls++
	s.number = number
	s.userID = userID

	return s.outcome, s.err
}

func TestUploadStoresNormalizedNumber(t *testing.T) {
	storage := &fakeStorage{outcome: Created}

	err := NewUploadService(storage).Upload(context.Background(), " 12345678903\n", 42)

	require.NoError(t, err)
	assert.Equal(t, "12345678903", storage.number)
	assert.Equal(t, int64(42), storage.userID)
	assert.Equal(t, 1, storage.calls)
}

func TestUploadRejectsNumberBeforeStorage(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   error
	}{
		{name: "пустое тело", number: "  \n", want: ErrEmptyNumber},
		{name: "не проходит Луна", number: "12345678902", want: ErrInvalidNumber},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeStorage{}

			err := NewUploadService(storage).Upload(context.Background(), test.number, 1)

			require.ErrorIs(t, err, test.want)
			assert.Zero(t, storage.calls)
		})
	}
}

func TestUploadMapsCreateOutcome(t *testing.T) {
	tests := []struct {
		name    string
		outcome CreateOutcome
		want    error
	}{
		{name: "создан", outcome: Created},
		{name: "уже принадлежит пользователю", outcome: AlreadyOwned, want: ErrAlreadyUploaded},
		{name: "принадлежит другому пользователю", outcome: OwnedByAnother, want: ErrOwnedByAnother},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeStorage{outcome: test.outcome}

			err := NewUploadService(storage).Upload(context.Background(), "12345678903", 42)

			if test.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.want)
			}
			assert.Equal(t, 1, storage.calls)
		})
	}
}

func TestUploadRejectsUnknownCreateOutcome(t *testing.T) {
	handlerSentinels := []error{
		ErrEmptyNumber,
		ErrInvalidNumber,
		ErrAlreadyUploaded,
		ErrOwnedByAnother,
	}
	tests := []struct {
		name    string
		outcome CreateOutcome
	}{
		{name: "нулевой", outcome: CreateOutcome(0)},
		{name: "неизвестный ненулевой", outcome: CreateOutcome(255)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeStorage{outcome: test.outcome}

			err := NewUploadService(storage).Upload(context.Background(), "12345678903", 42)

			assert.EqualError(t, err, fmt.Sprintf("create order: unknown outcome %d", test.outcome))
			for _, sentinel := range handlerSentinels {
				assert.NotErrorIs(t, err, sentinel)
			}
		})
	}
}

func TestUploadWrapsStorageFailure(t *testing.T) {
	storageErr := errors.New("storage is down")
	storage := &fakeStorage{err: storageErr}

	err := NewUploadService(storage).Upload(context.Background(), "12345678903", 1)

	require.ErrorIs(t, err, storageErr)
}
