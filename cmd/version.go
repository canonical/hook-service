// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"fmt"

	"github.com/canonical/hook-service/internal/version"
	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Get the application's version",
	Long:  `Get the application's version`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("App Version: %s\n", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
