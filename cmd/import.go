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

// importCmd imports users and groups from Salesforce
var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import users and groups from Salesforce",
	Long:  `Import users and groups from Salesforce into the local database`,
	Run:   runImport(),
}

func runImport() func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		sfDomain, _ := cmd.Flags().GetString("salesforce-domain")
		sfConsumerKey, _ := cmd.Flags().GetString("salesforce-consumer-key")
		sfConsumerSecret, _ := cmd.Flags().GetString("salesforce-consumer-secret")

		if sfDomain == "" || sfConsumerKey == "" || sfConsumerSecret == "" {
			cmd.PrintErr("Error: Salesforce credentials are required\n")
			os.Exit(1)
		}

		if err := importFromSalesforce(cmd.Context(), sfDomain, sfConsumerKey, sfConsumerSecret); err != nil {
			cmd.PrintErr(fmt.Sprintf("Import failed: %v\n", err))
			os.Exit(1)
		}
	}
}

func init() {
	importCmd.Flags().String("salesforce-domain", "", "Salesforce domain (e.g., login.salesforce.com)")
	importCmd.Flags().String("salesforce-consumer-key", "", "Salesforce OAuth consumer key")
	importCmd.Flags().String("salesforce-consumer-secret", "", "Salesforce OAuth consumer secret")

	_ = importCmd.MarkFlagRequired("salesforce-domain")
	_ = importCmd.MarkFlagRequired("salesforce-consumer-key")
	_ = importCmd.MarkFlagRequired("salesforce-consumer-secret")

	rootCmd.AddCommand(importCmd)
}

func importFromSalesforce(ctx context.Context, sfDomain, sfConsumerKey, sfConsumerSecret string) error {
	// Load environment configuration for database and other settings
	specs := new(config.EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		return fmt.Errorf("failed to load environment configuration: %v", err)
	}

	logger := logging.NewLogger(specs.LogLevel)
	defer logger.Sync()

	monitor := prometheus.NewMonitor("hook-service", logger)
	tracer := tracing.NewTracer(tracing.NewConfig(specs.TracingEnabled, specs.OtelGRPCEndpoint, specs.OtelHTTPEndpoint, logger))

	// Initialize database
	dbConfig := db.Config{
		DSN:             specs.DSN,
		MaxConns:        specs.DBMaxConns,
		MinConns:        specs.DBMinConns,
		MaxConnLifetime: specs.DBMaxConnLifetime,
		MaxConnIdleTime: specs.DBMaxConnIdleTime,
		TracingEnabled:  specs.TracingEnabled,
	}
	dbClient, err := db.NewDBClient(dbConfig, tracer, monitor, logger)
	if err != nil {
		return fmt.Errorf("failed to create database client: %v", err)
	}
	defer dbClient.Close()

	s := storage.NewStorage(dbClient, tracer, monitor, logger)

	// Initialize Salesforce client
	sfClient := salesforce.NewClient(sfDomain, sfConsumerKey, sfConsumerSecret)

	// Initialize Salesforce importer with storage layer
	imp := importer.NewSalesforceImporter(s, tracer, monitor, logger)

	// Perform import
	logger.Info("Starting Salesforce import...")
	processedUsers, err := imp.ImportUserGroups(ctx, sfClient)
	if err != nil {
		return fmt.Errorf("import failed: %v", err)
	}

	logger.Infof("Successfully imported %d users from Salesforce", processedUsers)
	fmt.Printf("Successfully imported %d users from Salesforce\n", processedUsers)

	return nil
}
