// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"github.com/spf13/cobra"

	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/importer"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring/prometheus"
	"github.com/canonical/hook-service/internal/salesforce"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import user-group data from an external source into the database",
	Long: `Import user-group data from an external source into the local database.

Currently supported drivers:
  - salesforce: imports users and their department/team groups from Salesforce

Example:
  hook-service import --driver salesforce --dsn "postgres://user:pass@host:5432/db"`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runImport(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "Import failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	importCmd.Flags().String("driver", "", "Import driver to use (salesforce)")
	importCmd.Flags().String("dsn", "", "PostgreSQL DSN connection string")
	importCmd.Flags().String("domain", "", "Salesforce domain")
	importCmd.Flags().String("consumer-key", "", "Salesforce consumer key")
	importCmd.Flags().String("consumer-secret", "", "Salesforce consumer secret")

	_ = importCmd.MarkFlagRequired("driver")
	_ = importCmd.MarkFlagRequired("dsn")

	rootCmd.AddCommand(importCmd)
}

func runImport(cmd *cobra.Command) error {
	driver, _ := cmd.Flags().GetString("driver")
	dsn, _ := cmd.Flags().GetString("dsn")

	specs := new(config.EnvSpec)
	// best-effort env loading, flags take precedence
	_ = envconfig.Process("", specs)

	logger := logging.NewLogger(specs.LogLevel)
	defer logger.Sync()

	monitor := prometheus.NewMonitor("hook-service", logger)
	tracer := tracing.NewTracer(tracing.NewConfig(false, "", "", logger))

	dbConfig := db.Config{DSN: dsn}
	dbClient, err := db.NewDBClient(dbConfig, tracer, monitor, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}
	defer dbClient.Close()

	s := storage.NewStorage(dbClient, tracer, monitor, logger)

	var importDriver importer.DriverInterface
	switch driver {
	case "salesforce":
		importDriver, err = buildSalesforceDriver(cmd, specs)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported driver: %q (supported: salesforce)", driver)
	}

	imp := importer.NewImporter(importDriver, s, logger)
	return imp.Run(context.Background())
}

func buildSalesforceDriver(cmd *cobra.Command, specs *config.EnvSpec) (*importer.SalesforceDriver, error) {
	domain, _ := cmd.Flags().GetString("domain")
	consumerKey, _ := cmd.Flags().GetString("consumer-key")
	consumerSecret, _ := cmd.Flags().GetString("consumer-secret")

	// Fall back to env vars if flags are not set
	if domain == "" {
		domain = specs.SalesforceDomain
	}
	if consumerKey == "" {
		consumerKey = specs.SalesforceConsumerKey
	}
	if consumerSecret == "" {
		consumerSecret = specs.SalesforceConsumerSecret
	}

	if domain == "" || consumerKey == "" || consumerSecret == "" {
		return nil, fmt.Errorf("salesforce driver requires --domain, --consumer-key, and --consumer-secret flags (or SALESFORCE_DOMAIN, SALESFORCE_CONSUMER_KEY, SALESFORCE_CONSUMER_SECRET env vars)")
	}

	sfClient := salesforce.NewClient(domain, consumerKey, consumerSecret)
	return importer.NewSalesforceDriver(sfClient), nil
}
