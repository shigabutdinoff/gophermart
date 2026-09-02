package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen_EmptyDSN(t *testing.T) {
	session, err := Open("")

	require.Error(t, err)
	assert.Equal(t, Session{}, session)
}

func TestOpen_MalformedDSN(t *testing.T) {
	session, err := Open("://malformed")

	require.Error(t, err)
	assert.Equal(t, Session{}, session)
}

func TestOpen_ParseErrorHidesCredentials(t *testing.T) {
	session, err := Open("postgresql://user:SECRETPW@bad host:5432/praktikum")

	assert.Equal(t, Session{}, session)
	assert.EqualError(t, err, "некорректная строка подключения к БД")
}

func TestOpen_LazyOpenWithoutDatabase(t *testing.T) {
	session, err := Open(
		"postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable",
	)

	require.NoError(t, err)

	sqlDB, err := session.Pool()
	require.NoError(t, err)
	assert.NotNil(t, sqlDB)
}

func TestOpen_EnablesGORMErrorTranslation(t *testing.T) {
	session, err := Open(
		"postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable",
	)

	require.NoError(t, err)
	require.NotNil(t, session.db)
	assert.True(t, session.db.Config.TranslateError)
}
