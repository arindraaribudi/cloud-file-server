package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers pgx as database/sql driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate ensures the workflow schema exists and applies embedded migrations.
// It uses workflow.schema_migrations so it cannot collide with the file
// server's public schema_migrations table.
func Migrate(ctx context.Context, dsn string) error {
	dbConn, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: sql.Open: %w", err)
	}
	defer func() { _ = dbConn.Close() }()

	if _, err := dbConn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS workflow"); err != nil {
		return fmt.Errorf("migrate: create schema: %w", err)
	}
	driver, err := migratepgx.WithInstance(dbConn, &migratepgx.Config{
		MigrationsTable: "schema_migrations",
		SchemaName:      "workflow",
	})
	if err != nil {
		return fmt.Errorf("migrate: pgx driver: %w", err)
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: iofs source: %w", err)
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

// WaitForTable waits until a table/relation exists. The workflow service may
// start before the file server has applied 0008_file_events.
func WaitForTable(ctx context.Context, pool *pgxpool.Pool, name string, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	for {
		var reg *string
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", name).Scan(&reg); err == nil && reg != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
