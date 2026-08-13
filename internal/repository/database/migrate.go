package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	schema "github.com/shigabutdinoff/gophermart/migrations"
)

// Migrate приводит схему к актуальной версии.
func Migrate(ctx context.Context, db *sql.DB) error {
	provider, err := newMigrationProvider(db)
	if err != nil {
		return err
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}

func newMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		schema.FS,
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return nil, fmt.Errorf("create migrations provider: %w", err)
	}

	return provider, nil
}
