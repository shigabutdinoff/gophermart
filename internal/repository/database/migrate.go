package database

import (
	"errors"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// MigrationsURL задаёт путь к каталогу миграций.
const MigrationsURL = "file://migrations"

// Migrate применяет миграции схемы и сообщает об изменениях.
func Migrate(sourceURL, dsn string) (bool, error) {
	m, err := migrate.New(sourceURL, dsn)
	if err != nil {
		return false, err
	}
	defer func() { _, _ = m.Close() }()

	return migrateUp(m)
}

type migrator interface {
	Up() error
}

func migrateUp(m migrator) (bool, error) {
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) || isEmptySource(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func isEmptySource(err error) bool {
	var pathErr *os.PathError
	return errors.As(err, &pathErr) &&
		pathErr.Op == "first" &&
		errors.Is(pathErr.Err, os.ErrNotExist)
}
