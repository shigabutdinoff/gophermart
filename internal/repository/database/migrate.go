package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"

	schema "github.com/shigabutdinoff/gophermart/migrations"
)

// Migrate приводит схему к актуальной версии.
// Свои таблицы очередь заданий держит отдельной линией миграций.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := migrateDomain(ctx, db); err != nil {
		return err
	}

	return migrateQueue(ctx, db)
}

func migrateDomain(ctx context.Context, db *sql.DB) error {
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

// migrateQueue катит миграции очереди, об исходе отчитывается вызывающий.
func migrateQueue(ctx context.Context, db *sql.DB) error {
	migrator, err := rivermigrate.New(
		riverdatabasesql.New(db),
		&rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)},
	)
	if err != nil {
		return fmt.Errorf("create queue migrator: %w", err)
	}

	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("apply queue migrations: %w", err)
	}

	return nil
}
