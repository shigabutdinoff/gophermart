package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnection_EmptyDSN(t *testing.T) {
	db, err := Connection("")

	require.Error(t, err)
	assert.Nil(t, db)
}

func TestConnection_MalformedDSN(t *testing.T) {
	db, err := Connection("://malformed")

	require.Error(t, err)
	assert.Nil(t, db)
}

func TestConnection_ParseErrorHidesCredentials(t *testing.T) {
	db, err := Connection("postgresql://user:SECRETPW@bad host:5432/praktikum")

	assert.Nil(t, db)
	assert.EqualError(t, err, "некорректная строка подключения к БД")
}

func TestConnection_LazyOpenWithoutDatabase(t *testing.T) {
	db, err := Connection(
		"postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable",
	)

	require.NoError(t, err)
	require.NotNil(t, db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	assert.NotNil(t, sqlDB)
}

func TestConnection_EnablesGORMErrorTranslation(t *testing.T) {
	db, err := Connection(
		"postgresql://postgres:postgres@localhost:1/praktikum?sslmode=disable",
	)

	require.NoError(t, err)
	require.NotNil(t, db)
	assert.True(t, db.Config.TranslateError)
}
