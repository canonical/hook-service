// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package testhelpers

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/canonical/hook-service/migrations"
)

// migrationTimeout bounds a single RunMigrations run.
const migrationTimeout = 30 * time.Second

// RunMigrations applies all embedded goose migrations to the database at
// connStr. It builds a goose.Provider per call (mirroring cmd/migrate.go)
// instead of using the package-level goose API, so parallel tests cannot
// race on goose's global base FS and dialect. Any failure is fatal to the
// calling test.
func RunMigrations(t *testing.T, connStr string) {
	t.Helper()

	cfg, err := pgx.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("failed to parse DSN: %v", err)
	}

	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.EmbedMigrations)
	if err != nil {
		t.Fatalf("failed to create goose provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}
}
