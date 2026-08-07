package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	schema "github.com/shigabutdinoff/gophermart/migrations"
)

// migrationsDir указывает корень встроенной файловой системы миграций
const migrationsDir = "."

// Migrate приводит схему к актуальной версии.
func Migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(schema.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migrations dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}
