package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_MissingDatabaseIsControlled(t *testing.T) {
	db, err := NewSession(nil).WithContext(context.Background())

	require.ErrorIs(t, err, ErrUnavailable)
	assert.Nil(t, db)
}

func TestSession_MissingDatabaseHasNoPool(t *testing.T) {
	pool, err := Session{}.Pool()

	require.ErrorIs(t, err, ErrUnavailable)
	assert.Nil(t, pool)
}

func TestSession_BindsConnectionToContext(t *testing.T) {
	session, err := Open(
		"postgres://gophermart:password@127.0.0.1:1/gophermart?sslmode=disable",
	)
	require.NoError(t, err)
	sqlDB, err := session.Pool()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	ctx := context.WithValue(context.Background(), sessionContextKey{}, "value")

	bound, err := session.WithContext(ctx)

	require.NoError(t, err)
	require.NotNil(t, bound)
	assert.Same(t, ctx, bound.Statement.Context)
	assert.NotSame(t, session.db, bound)
}

type sessionContextKey struct{}
