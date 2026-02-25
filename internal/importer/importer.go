// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package importer

import (
	"context"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/types"
)

// StorageInterface defines the storage operations required by the Importer.
type StorageInterface interface {
	CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error)
	GetGroupByName(ctx context.Context, name string) (*types.Group, error)
	AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error
}

// Importer handles the bulk import of user-group mappings from an external
// driver into the local database.
type Importer struct {
	driver  DriverInterface
	storage StorageInterface
	logger  logging.LoggerInterface
}

// NewImporter creates a new Importer with the given driver, storage, and logger.
func NewImporter(driver DriverInterface, storage StorageInterface, logger logging.LoggerInterface) *Importer {
	return &Importer{
		driver:  driver,
		storage: storage,
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
		group, err := i.storage.GetGroupByName(ctx, groupName)

		// if err is not nil, it might mean the group does not exist, so we create it
		if group == nil || err != nil {
			group, err = i.storage.CreateGroup(ctx, &types.Group{
				Name:     groupName,
				TenantId: "default",
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
