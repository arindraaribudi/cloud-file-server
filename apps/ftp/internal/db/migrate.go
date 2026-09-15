package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" as database/sql driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any pending up migrations from the embedded SQL directory.
// Idempotent: golang-migrate records applied versions in schema_migrations
// and skips them on subsequent runs. Safe to call on every boot.
func Migrate(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: iofs source: %w", err)
	}

	dbConn, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: sql.Open: %w", err)
	}
	defer func() { _ = dbConn.Close() }()

	driver, err := migratepgx.WithInstance(dbConn, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("migrate: pgx driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("migrate: instance: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
