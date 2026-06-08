// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// groupsCmd is the parent command for group membership management operations.
var groupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "Manage group memberships",
	Long:  `Manage group memberships directly in the database.`,
}

// groupsAddUsersCmd adds users to a group.
var groupsAddUsersCmd = &cobra.Command{
	Use:   "add-users <group-id>",
	Short: "Add users to a group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := runGroupsAddUsers(cmd, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// groupsRemoveUsersCmd removes users from a group.
var groupsRemoveUsersCmd = &cobra.Command{
	Use:   "remove-users <group-id>",
	Short: "Remove users from a group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := runGroupsRemoveUsers(cmd, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// groupsListUsersCmd lists all users in a group.
var groupsListUsersCmd = &cobra.Command{
	Use:   "list-users <group-id>",
	Short: "List all users in a group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := runGroupsListUsers(cmd, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	for _, sub := range []*cobra.Command{groupsAddUsersCmd, groupsRemoveUsersCmd, groupsListUsersCmd} {
		sub.Flags().String("dsn", "", "PostgreSQL DSN connection string")
		sub.Flags().StringP("format", "f", "text", "Output format (text or json)")
		_ = sub.MarkFlagRequired("dsn")
	}

	groupsAddUsersCmd.Flags().StringSliceP("user", "u", nil, "User ID to add (repeatable, or comma-separated)")
	_ = groupsAddUsersCmd.MarkFlagRequired("user")
	groupsRemoveUsersCmd.Flags().StringSliceP("user", "u", nil, "User ID to remove (repeatable, or comma-separated)")

	groupsCmd.AddCommand(groupsAddUsersCmd)
	groupsCmd.AddCommand(groupsRemoveUsersCmd)
	groupsCmd.AddCommand(groupsListUsersCmd)

	rootCmd.AddCommand(groupsCmd)
}

// runGroupsAddUsers adds users to a group.
func runGroupsAddUsers(cmd *cobra.Command, groupID string) error {
	s, cleanup, err := newStorageFromCmd(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	userIDs, _ := cmd.Flags().GetStringSlice("user")

	if err := s.AddUsersToGroup(cmd.Context(), groupID, userIDs); err != nil {
		return fmt.Errorf("failed to add users to group %q: %v", groupID, err)
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{
			"group_id":    groupID,
			"users_added": len(userIDs),
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Added %d users to group %s\n", len(userIDs), groupID)
	return nil
}

// runGroupsRemoveUsers removes users from a group.
func runGroupsRemoveUsers(cmd *cobra.Command, groupID string) error {
	s, cleanup, err := newStorageFromCmd(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	userIDs, _ := cmd.Flags().GetStringSlice("user")

	if err := s.RemoveUsersFromGroup(cmd.Context(), groupID, userIDs); err != nil {
		return fmt.Errorf("failed to remove users from group %q: %v", groupID, err)
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{
			"group_id":      groupID,
			"users_removed": len(userIDs),
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Removed %d users from group %s\n", len(userIDs), groupID)
	return nil
}

// runGroupsListUsers lists all users in a group.
func runGroupsListUsers(cmd *cobra.Command, groupID string) error {
	s, cleanup, err := newStorageFromCmd(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	userIDs, err := s.ListUsersInGroup(cmd.Context(), groupID)
	if err != nil {
		return fmt.Errorf("failed to list users in group %q: %v", groupID, err)
	}

	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		if userIDs == nil {
			userIDs = []string{}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(userIDs)
	}

	for _, uid := range userIDs {
		fmt.Fprintln(cmd.OutOrStdout(), uid)
	}
	return nil
}
