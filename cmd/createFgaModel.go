// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/openfga/go-sdk/client"
	"github.com/spf13/cobra"

	"github.com/canonical/hook-service/internal/authorization"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/tracing"
)

const StoreName = "hook-service"

// createFgaModelCmd represents the createFgaModel command
var createFgaModelCmd = &cobra.Command{
	Use:   "create-fga-model",
	Short: "Creates an openfga model",
	Long:  `Creates an openfga model`,
	Run: func(cmd *cobra.Command, args []string) {
		apiUrl, _ := cmd.Flags().GetString("fga-api-url")
		apiToken, _ := cmd.Flags().GetString("fga-api-token")
		storeId, _ := cmd.Flags().GetString("fga-store-id")
		format, _ := cmd.Flags().GetString("format")

		modelId, finalStoreId, err := createModel(apiUrl, apiToken, storeId)
		if err != nil {
			cmd.PrintErrln(err)
			os.Exit(1)
		}

		if format == "json" {
			output := struct {
				StoreId string `json:"store_id"`
				ModelId string `json:"model_id"`
			}{
				StoreId: finalStoreId,
				ModelId: modelId,
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(output); err != nil {
				cmd.PrintErrln(fmt.Errorf("failed to encode output: %v", err))
				os.Exit(1)
			}
		} else {
			cmd.Printf("Created model: %s\n", modelId)
			if storeId == "" {
				cmd.Printf("Created store: %s\n", finalStoreId)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(createFgaModelCmd)

	createFgaModelCmd.Flags().String("fga-api-url", "", "The openfga API URL")
	createFgaModelCmd.Flags().String("fga-api-token", "", "The openfga API token")
	createFgaModelCmd.Flags().String("fga-store-id", "", "The openfga store to create the model in, if empty one will be created")
	createFgaModelCmd.Flags().String("format", "text", "Output format (text or json)")
	createFgaModelCmd.MarkFlagRequired("fga-api-url")
	createFgaModelCmd.MarkFlagRequired("fga-api-token")
}

func createModel(apiUrl, apiToken, storeId string) (string, string, error) {
	ctx := context.Background()

	logger := logging.NewNoopLogger()
	tracer := tracing.NewNoopTracer()
	monitor := monitoring.NewNoopMonitor("", logger)

	scheme, host, err := parseURL(apiUrl)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse url: %w", err)
	}

	// skip validation for openfga object
	cfg := openfga.Config{
		ApiScheme:   scheme,
		ApiHost:     host,
		StoreID:     storeId,
		ApiToken:    apiToken,
		AuthModelID: "",
		Debug:       false,
		Tracer:      tracer,
		Monitor:     monitor,
		Logger:      logger,
	}

	fgaClient := openfga.NewClient(&cfg)

	if storeId == "" {
		storeId, err = fgaClient.CreateStore(ctx, StoreName)

		if err != nil {
			return "", "", fmt.Errorf("failed to create store: %w", err)
		}

		fgaClient.SetStoreID(ctx, storeId)
	}

	authzModel := authorization.NewAuthorizationModelProvider("v0").
		GetModel()

	modelId, err := fgaClient.WriteModel(
		context.Background(),
		&client.ClientWriteAuthorizationModelRequest{
			TypeDefinitions: authzModel.TypeDefinitions,
			SchemaVersion:   authzModel.SchemaVersion,
			Conditions:      authzModel.Conditions,
		},
	)

	if err != nil {
		return "", "", fmt.Errorf("failed to write model: %w", err)
	}

	return modelId, storeId, nil
}

func parseURL(s string) (string, string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", "", err
	}
	return u.Scheme, u.Host, nil
}
