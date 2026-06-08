// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/types"
)

// StorageInterface defines the storage operations required by the Importer.
type StorageInterface interface {
	CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error)
	GetGroupByName(ctx context.Context, name, tenantID string) (*types.Group, error)
	AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error
	ListGroupsByPrefix(ctx context.Context, prefix, tenantID string) ([]*types.Group, error)
	SyncGroupMembers(ctx context.Context, groupID string, userIDs []string) error
	DeleteGroup(ctx context.Context, id string) error
}

// Importer handles the bulk import of user-group mappings from an external
// driver into the local database.
type Importer struct {
	driver  DriverInterface
	storage StorageInterface
	authz   AuthorizerInterface
	logger  logging.LoggerInterface
}

// NewImporter creates a new Importer with the given driver, storage, authz, and logger.
func NewImporter(driver DriverInterface, storage StorageInterface, authz AuthorizerInterface, logger logging.LoggerInterface) *Importer {
	return &Importer{
		driver:  driver,
		storage: storage,
		authz:   authz,
		logger:  logger,
	}
}

// Run executes the import process:
// 1. Fetches all user-group mappings from the driver.
// 2. Finds or creates groups in the database (deduplicating by name).
// 3. Adds users to their respective groups.
func (i *Importer) Run(ctx context.Context) error {
	mappings, err := i.driver.FetchAllUserGroups(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch user groups from driver: %w", err)
	}

	i.logger.Infof("Fetched %d user-group mappings from driver", len(mappings))

	// Deduplicate groups and collect users per group
	groupUsers := make(map[string][]string)
	for _, m := range mappings {
		name := fmt.Sprintf("%s:%s", i.driver.Prefix(), m.GroupName)
		groupUsers[name] = append(groupUsers[name], m.UserID)
	}

	created := 0
	for groupName, userIDs := range groupUsers {
		group, err := i.storage.GetGroupByName(ctx, groupName, storage.DefaultTenantID)

		// if err is not nil, it might mean the group does not exist, so we create it
		if group == nil || err != nil {
			group, err = i.storage.CreateGroup(ctx, &types.Group{
				Name:     groupName,
				TenantId: storage.DefaultTenantID,
			})
			if err != nil {
				i.logger.Errorf("Failed to create group %q: %v", groupName, err)
				continue
			}
		}

		if err := i.storage.AddUsersToGroup(ctx, group.ID, userIDs); err != nil {
			i.logger.Errorf("Failed to add %d users to group %q: %v", len(userIDs), groupName, err)
			continue
		}

		created++
		i.logger.Infof("Imported group %q with %d users", groupName, len(userIDs))
	}

	i.logger.Infof("Import complete: %d groups processed", created)
	return nil
}

// Sync reconciles the database with the current state of the external driver.
// It only affects groups prefixed with the driver's prefix — local/admin-created
// groups are never touched. Groups present in the driver but not the DB are created;
// groups present in the driver and DB are member-reconciled; groups present in the
// DB but not the driver are deleted.
func (i *Importer) Sync(ctx context.Context) error {
	mappings, err := i.driver.FetchAllUserGroups(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch user groups from driver: %w", err)
	}

	i.logger.Infof("Fetched %d user-group mappings from driver for sync", len(mappings))

	prefix := i.driver.Prefix()

	// Build driverGroups: prefixed group name → []userID
	driverGroups := make(map[string][]string)
	for _, m := range mappings {
		name := fmt.Sprintf("%s:%s", prefix, m.GroupName)
		driverGroups[name] = append(driverGroups[name], m.UserID)
	}

	// Fetch existing groups matching the driver prefix from DB
	existingGroups, err := i.storage.ListGroupsByPrefix(ctx, prefix+":", storage.DefaultTenantID)
	if err != nil {
		return fmt.Errorf("failed to list existing groups by prefix: %v", err)
	}

	// Build O(1) lookup map: name → *Group
	existingByName := make(map[string]*types.Group, len(existingGroups))
	for _, g := range existingGroups {
		existingByName[g.Name] = g
	}

	synced, created, deleted := 0, 0, 0
	failures := 0

	// Reconcile driver groups against DB
	for name, userIDs := range driverGroups {
		if existing, ok := existingByName[name]; ok {
			if err := i.storage.SyncGroupMembers(ctx, existing.ID, userIDs); err != nil {
				i.logger.Errorf("Failed to sync members for group %q: %v", name, err)
				failures++
				continue
			}
			synced++
			i.logger.Infof("Synced group %q with %d users", name, len(userIDs))
		} else {
			group, err := i.storage.CreateGroup(ctx, &types.Group{
				Name:     name,
				TenantId: storage.DefaultTenantID,
			})
			if err != nil {
				i.logger.Errorf("Failed to create group %q: %v", name, err)
				failures++
				continue
			}
			if err := i.storage.AddUsersToGroup(ctx, group.ID, userIDs); err != nil {
				i.logger.Errorf("Failed to add %d users to group %q: %v", len(userIDs), name, err)
				failures++
				continue
			}
			created++
			i.logger.Infof("Created group %q with %d users", name, len(userIDs))
		}
	}

	// Delete stale groups (present in DB but not in driver data)
	for name, group := range existingByName {
		if _, ok := driverGroups[name]; !ok {
			if err := i.storage.DeleteGroup(ctx, group.ID); err != nil {
				i.logger.Errorf("Failed to delete stale group %q: %v", name, err)
				failures++
				continue
			}
			if err := i.authz.DeleteGroup(ctx, group.ID); err != nil {
				i.logger.Errorf("Failed to remove authorization tuples for stale group %q: %v", name, err)
				failures++
			}
			deleted++
			i.logger.Infof("Deleted stale group %q", name)
		}
	}

	i.logger.Infof("Sync complete: %d synced, %d created, %d deleted", synced, created, deleted)
	if failures > 0 {
		return fmt.Errorf("sync completed with %d failure(s); see logs for details", failures)
	}
	return nil
}
