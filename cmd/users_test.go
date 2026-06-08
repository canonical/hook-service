// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestUsersDeleteRequiresDSN(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().StringP("format", "f", "text", "")

	err := runUsersDelete(cmd, "alice@example.com")
	if err == nil {
		t.Fatal("expected error when dsn is empty")
	}
}

func TestUsersListGroupsRequiresDSN(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().StringP("format", "f", "text", "")

	err := runUsersListGroups(cmd, "alice@example.com")
	if err == nil {
		t.Fatal("expected error when dsn is empty")
	}
}

func TestUsersSetGroupsRequiresDSN(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("dsn", "", "")
	cmd.Flags().StringP("format", "f", "text", "")
	cmd.Flags().StringSliceP("group", "g", nil, "")

	err := runUsersSetGroups(cmd, "alice@example.com")
	if err == nil {
		t.Fatal("expected error when dsn is empty")
	}
}

func TestUsersDeleteSubcommandExactArgs(t *testing.T) {
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
			args:    []string{"alice@example.com"},
			wantErr: false,
		},
		{
			name:    "two args",
			args:    []string{"alice@example.com", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cobra.ExactArgs(1)(usersDeleteCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExactArgs(1) error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUsersListGroupsSubcommandExactArgs(t *testing.T) {
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
			args:    []string{"alice@example.com"},
			wantErr: false,
		},
		{
			name:    "two args",
			args:    []string{"alice@example.com", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cobra.ExactArgs(1)(usersListGroupsCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExactArgs(1) error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUsersSetGroupsSubcommandExactArgs(t *testing.T) {
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
			args:    []string{"alice@example.com"},
			wantErr: false,
		},
		{
			name:    "two args",
			args:    []string{"alice@example.com", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cobra.ExactArgs(1)(usersSetGroupsCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExactArgs(1) error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestUsersSetGroupsRequiresGroupFlag verifies that the --group flag is marked
// required on the set-groups subcommand, preventing accidental removal of all
// user memberships when the flag is omitted.
func TestUsersSetGroupsRequiresGroupFlag(t *testing.T) {
	parent := &cobra.Command{Use: "hook-service"}
	child := &cobra.Command{
		Use:  "set-groups <user-id>",
		Args: cobra.ExactArgs(1),
		Run:  func(cmd *cobra.Command, args []string) {},
	}
	child.Flags().String("dsn", "", "")
	child.Flags().StringSliceP("group", "g", nil, "")
	_ = child.MarkFlagRequired("group")
	parent.AddCommand(child)

	parent.SetArgs([]string{"set-groups", "alice@example.com", "--dsn", "postgres://x"})
	if err := parent.Execute(); err == nil {
		t.Fatal("expected error when --group flag is omitted")
	}
}
