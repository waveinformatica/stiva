package storage

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies the embedded SQL migrations to the PostgreSQL database
// identified by dsn using golang-migrate. The baseline migration is written with
// IF NOT EXISTS so it is safe to run against a database already created by the
// legacy idempotent startup DDL; subsequent (future) migrations then run exactly
// once and are tracked in the schema_migrations table.
func Migrate(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("storage: open migrate db: %w", err)
	}
	defer db.Close()

	src, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("storage: migration source: %w", err)
	}
	pdb, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("storage: migrate postgres instance: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", pdb)
	if err != nil {
		return fmt.Errorf("storage: migrate instance: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("storage: apply migrations: %w", err)
	}
	return nil
}
