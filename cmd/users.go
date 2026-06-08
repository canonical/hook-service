// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring/prometheus"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
)

// usersCmd is the parent command for user management operations.
var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage user group memberships",
	Long:  `Manage user group memberships directly in the database.`,
}

// usersDeleteCmd removes a user from all groups.
var usersDeleteCmd = &cobra.Command{
	Use:   "delete <user-id>",
	Short: "Remove a user from all groups",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := runUsersDelete(cmd, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// usersListGroupsCmd lists all groups a user belongs to.
var usersListGroupsCmd = &cobra.Command{
	Use:   "list-groups <user-id>",
	Short: "List all groups a user belongs to",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := runUsersListGroups(cmd, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// usersSetGroupsCmd replaces a user's group memberships.
var usersSetGroupsCmd = &cobra.Command{
	Use:   "set-groups <user-id>",
	Short: "Replace a user's group memberships with the specified groups",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := runUsersSetGroups(cmd, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	for _, sub := range []*cobra.Command{usersDeleteCmd, usersListGroupsCmd, usersSetGroupsCmd} {
		sub.Flags().String("dsn", "", "PostgreSQL DSN connection string")
		sub.Flags().StringP("format", "f", "text", "Output format (text or json)")
		_ = sub.MarkFlagRequired("dsn")
	}

	usersSetGroupsCmd.Flags().StringSliceP("group", "g", nil, "Group ID to assign (repeatable, or comma-separated)")
	_ = usersSetGroupsCmd.MarkFlagRequired("group")

	usersCmd.AddCommand(usersDeleteCmd)
	usersCmd.AddCommand(usersListGroupsCmd)
	usersCmd.AddCommand(usersSetGroupsCmd)

	rootCmd.AddCommand(usersCmd)
}

// newStorageFromCmd creates a storage client from the --dsn flag on cmd.
func newStorageFromCmd(cmd *cobra.Command) (*storage.Storage, func(), error) {
	dsn, _ := cmd.Flags().GetString("dsn")
	if dsn == "" {
		return nil, nil, fmt.Errorf("--dsn is required")
	}

	logger := logging.NewLogger("error")
	monitor := prometheus.NewMonitor("hook-service", logger)
	tracer := tracing.NewTracer(tracing.NewConfig(false, "", "", logger))

	dbConfig := db.Config{
		DSN:      dsn,
		MaxConns: 10,
		MinConns: 1,
	}

	dbClient, err := db.NewDBClient(dbConfig, tracer, monitor, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	s := storage.NewStorage(dbClient, tracer, monitor, logger)
	cleanup := func() {
		dbClient.Close()
		logger.Sync()
	}

	return s, cleanup, nil
}

// runUsersDelete removes a user from all groups.
func runUsersDelete(cmd *cobra.Command, userID string) error {
	s, cleanup, err := newStorageFromCmd(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := s.RemoveUserFromAllGroups(cmd.Context(), userID); err != nil {
		return fmt.Errorf("failed to delete user %q: %v", userID, err)
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
			"user_id": userID,
			"status":  "deleted",
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Deleted user %s from all groups\n", userID)
	return nil
}

// runUsersListGroups lists all groups a user belongs to.
func runUsersListGroups(cmd *cobra.Command, userID string) error {
	s, cleanup, err := newStorageFromCmd(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	groups, err := s.GetGroupsForUser(cmd.Context(), userID)
	if err != nil {
		return fmt.Errorf("failed to list groups for user %q: %v", userID, err)
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		if groups == nil {
			groups = []*types.Group{}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(groups)
	}

	for _, g := range groups {
		fmt.Fprintln(cmd.OutOrStdout(), g.Name)
	}
	return nil
}

// runUsersSetGroups replaces a user's group memberships.
func runUsersSetGroups(cmd *cobra.Command, userID string) error {
	s, cleanup, err := newStorageFromCmd(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	groupIDs, _ := cmd.Flags().GetStringSlice("group")

	if err := s.UpdateGroupsForUser(cmd.Context(), userID, groupIDs); err != nil {
		return fmt.Errorf("failed to set groups for user %q: %v", userID, err)
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{
			"user_id": userID,
			"groups":  groupIDs,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated groups for user %s\n", userID)
	return nil
}
