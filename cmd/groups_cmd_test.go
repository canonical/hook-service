// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestGroupsAddUsersRequiresDSN(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().StringP("format", "f", "text", "")
	cmd.Flags().StringSliceP("user", "u", nil, "")

	err := runGroupsAddUsers(cmd, "group-id-1")
	if err == nil {
		t.Fatal("expected error when dsn is empty")
	}
}

func TestGroupsRemoveUsersRequiresDSN(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().StringP("format", "f", "text", "")
	cmd.Flags().StringSliceP("user", "u", nil, "")

	err := runGroupsRemoveUsers(cmd, "group-id-1")
	if err == nil {
		t.Fatal("expected error when dsn is empty")
	}
}

func TestGroupsListUsersRequiresDSN(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().StringP("format", "f", "text", "")

	err := runGroupsListUsers(cmd, "group-id-1")
	if err == nil {
		t.Fatal("expected error when dsn is empty")
	}
}

func TestGroupsAddUsersSubcommandExactArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "no args",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "one arg",
			args:    []string{"group-id-1"},
			wantErr: false,
		},
		{
			name:    "two args",
			args:    []string{"group-id-1", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cobra.ExactArgs(1)(groupsAddUsersCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExactArgs(1) error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGroupsRemoveUsersSubcommandExactArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "no args",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "one arg",
			args:    []string{"group-id-1"},
			wantErr: false,
		},
		{
			name:    "two args",
			args:    []string{"group-id-1", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cobra.ExactArgs(1)(groupsRemoveUsersCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExactArgs(1) error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGroupsListUsersSubcommandExactArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "no args",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "one arg",
			args:    []string{"group-id-1"},
			wantErr: false,
		},
		{
			name:    "two args",
			args:    []string{"group-id-1", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cobra.ExactArgs(1)(groupsListUsersCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExactArgs(1) error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestImportCmdSyncFlag(t *testing.T) {
	tests := []struct {
		name    string
		driver  string
		wantErr bool
	}{
		{
			name:    "unsupported driver with sync",
			driver:  "unknown",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("driver", "", "")
			cmd.Flags().String("dsn", "", "")
			cmd.Flags().String("domain", "", "")
			cmd.Flags().String("consumer-key", "", "")
			cmd.Flags().String("consumer-secret", "", "")
			cmd.Flags().Bool("sync", false, "")

			cmd.Flags().Set("driver", tt.driver)
			cmd.Flags().Set("dsn", "postgres://user:pass@localhost:5432/db")
			cmd.Flags().Set("sync", "true")

			err := runImport(cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("runImport() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestGroupsAddUsersRequiresUserFlag verifies that the --user flag is marked
// required on the add-users subcommand, preventing silent no-ops when it is omitted.
func TestGroupsAddUsersRequiresUserFlag(t *testing.T) {
	parent := &cobra.Command{Use: "hook-service"}
	child := &cobra.Command{
		Use:  "add-users <group-id>",
		Args: cobra.ExactArgs(1),
		Run:  func(cmd *cobra.Command, args []string) {},
	}
	child.Flags().String("dsn", "", "")
	child.Flags().StringSliceP("user", "u", nil, "")
	_ = child.MarkFlagRequired("user")
	parent.AddCommand(child)

	parent.SetArgs([]string{"add-users", "group-id-1", "--dsn", "postgres://x"})
	if err := parent.Execute(); err == nil {
		t.Fatal("expected error when --user flag is omitted")
	}
}
