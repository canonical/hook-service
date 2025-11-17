// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	"github.com/canonical/hook-service/migrations"
)

// migrateCmd performs DB migrations
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	Long:  `Run database migrations`,
	Args:  customValidArgs(),
	Run:   runMigrate(),
}

func customValidArgs() func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}

		if err := cobra.RangeArgs(0, 2)(cmd, args); err != nil {
			return err
		}

		first := args[0]
		switch first {
		case "up", "down", "status", "check":
			// valid first argument
		default:
			return fmt.Errorf("invalid first argument: %q", first)
		}

		// If two arguments are provided, the first must be "down" and second a non-negative int
		if len(args) == 2 {
			if first != "down" {
				return fmt.Errorf("invalid argument combination: %q", args)
			}

			if version, err := strconv.Atoi(args[1]); err != nil || version < 0 {
				return fmt.Errorf("invalid version number: %q", args[1])
			}
		}

		return nil
	}
}

func runMigrate() func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		command := "up"
		if len(args) > 0 {
			command = args[0]
		}

		version := -1
		if len(args) > 1 {
			version, _ = strconv.Atoi(args[1])
		}

		dsn, _ := cmd.Flags().GetString("dsn")
		format, _ := cmd.Flags().GetString("format")

		if err := migrate(cmd, dsn, command, format, version); err != nil {
			cmd.PrintErr(err)
			os.Exit(1)
		}
	}
}

func init() {
	migrateCmd.Flags().String("dsn", "", "PostgreSQL DSN connection string")
	migrateCmd.Flags().StringP("format", "f", "text", "Output format (text or json)")
	_ = migrateCmd.MarkFlagRequired("dsn")

	rootCmd.AddCommand(migrateCmd)
}

func migrate(cmd *cobra.Command, dsn, command, format string, version int) error {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("DSN validation failed, shutting down, err: %v", err)
	}

	db := stdlib.OpenDB(*config)

	if err := db.PingContext(cmd.Context()); err != nil {
		return fmt.Errorf("DB connection failed, shutting down, err: %v", err)
	}
	goose.SetBaseFS(migrations.EmbedMigrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	var opts []goose.ProviderOption
	if format == "json" {
		opts = append(opts, goose.WithLogger(goose.NopLogger()))
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.EmbedMigrations, opts...)
	if err != nil {
		return fmt.Errorf("failed to create goose provider: %w", err)
	}

	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	switch command {
	case "up":
		return runUp(ctx, provider, format, out)
	case "down":
		return runDown(ctx, provider, version, format, out)
	case "status":
		return runStatus(ctx, provider, format, out)
	case "check":
		return runCheck(ctx, provider, format, out)
	}

	return nil
}

func runUp(ctx context.Context, provider *goose.Provider, format string, out io.Writer) error {
	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	if format == "json" {
		if results == nil {
			results = []*goose.MigrationResult{}
		}
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"applied": results,
		})
	}
	return nil
}

func runDown(ctx context.Context, provider *goose.Provider, version int, format string, out io.Writer) error {
	var results []*goose.MigrationResult
	var err error

	if version == -1 {
		var result *goose.MigrationResult
		result, err = provider.Down(ctx)
		if err == nil {
			results = append(results, result)
		}
	} else {
		results, err = provider.DownTo(ctx, int64(version))
	}

	if err != nil {
		return err
	}

	if format == "json" {
		if results == nil {
			results = []*goose.MigrationResult{}
		}
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"applied": results,
		})
	}
	return nil
}

func runStatus(ctx context.Context, provider *goose.Provider, format string, out io.Writer) error {
	statuses, err := provider.Status(ctx)
	if err != nil {
		return err
	}
	if format == "json" {
		return json.NewEncoder(out).Encode(statuses)
	}

	log.Println("    Applied At                  Migration")
	log.Println("    =======================================")
	for _, s := range statuses {
		appliedAt := "Pending"
		if s.State == goose.StateApplied {
			appliedAt = s.AppliedAt.Format(time.RFC3339)
		}
		log.Printf("    %-24s -- %s\n", appliedAt, s.Source.Path)
	}
	return nil
}

func runCheck(ctx context.Context, provider *goose.Provider, format string, out io.Writer) error {
	hasPending, err := provider.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("failed to check pending migrations: %w", err)
	}

	if hasPending {
		current, err := provider.GetDBVersion(ctx)
		if err != nil {
			return fmt.Errorf("migrations are pending (failed to get current version: %v)", err)
		}
		if format == "json" {
			return json.NewEncoder(out).Encode(map[string]interface{}{
				"status":  "pending",
				"version": current,
			})
		}
		return fmt.Errorf("migrations are pending: current version %d", current)
	}

	current, err := provider.GetDBVersion(ctx)
	if format == "json" {
		status := "ok"
		if err != nil {
			status = "unknown"
		}
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"status":  status,
			"version": current,
		})
	}

	if err != nil {
		fmt.Println("Database is up to date")
	} else {
		fmt.Printf("Database is up to date (version %d)\n", current)
	}
	return nil
}
