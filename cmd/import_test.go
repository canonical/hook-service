// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestImportCmdRequiresDriver(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("driver", "", "")
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().String("domain", "", "")
	cmd.Flags().String("consumer-key", "", "")
	cmd.Flags().String("consumer-secret", "", "")

	// Set DSN but no driver
	cmd.Flags().Set("dsn", "postgres://user:pass@localhost:5432/db")

	err := runImport(cmd)
	if err == nil {
		t.Fatal("expected error when driver is empty")
	}
}

func TestImportCmdUnsupportedDriver(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("driver", "", "")
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().String("domain", "", "")
	cmd.Flags().String("consumer-key", "", "")
	cmd.Flags().String("consumer-secret", "", "")

	cmd.Flags().Set("driver", "unknown")
	cmd.Flags().Set("dsn", "postgres://user:pass@localhost:5432/db")

	err := runImport(cmd)
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}

func TestImportCmdSalesforceRequiresCredentials(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("driver", "", "")
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().String("domain", "", "")
	cmd.Flags().String("consumer-key", "", "")
	cmd.Flags().String("consumer-secret", "", "")

	cmd.Flags().Set("driver", "salesforce")
	cmd.Flags().Set("dsn", "postgres://user:pass@localhost:5432/db")

	err := runImport(cmd)
	if err == nil {
		t.Fatal("expected error when salesforce credentials are missing")
	}
}
