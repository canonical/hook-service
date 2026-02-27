// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package storage

import (
	"context"

	"github.com/canonical/hook-service/internal/types"
)

type StorageInterface interface {
	// Group CRUD operations
	ListGroups(ctx context.Context) ([]*types.Group, error)
	CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error)
	GetGroup(ctx context.Context, id string) (*types.Group, error)
	GetGroupByName(ctx context.Context, name string) (*types.Group, error)
	UpdateGroup(ctx context.Context, id string, group *types.Group) (*types.Group, error)
	DeleteGroup(ctx context.Context, id string) error

	// Group membership operations
	AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error
	ListUsersInGroup(ctx context.Context, groupID string) ([]string, error)
	RemoveUsersFromGroup(ctx context.Context, groupID string, users []string) error

	// User-centric group operations
	GetGroupsForUser(ctx context.Context, userID string) ([]*types.Group, error)
	UpdateGroupsForUser(ctx context.Context, userID string, groupIDs []string) error

	// Application authorization operations
	GetAllowedApps(ctx context.Context, groupID string) ([]string, error)
	AddAllowedApp(ctx context.Context, groupID string, appID string) error
	AddAllowedApps(ctx context.Context, groupID string, appIDs []string) error
	RemoveAllowedApp(ctx context.Context, groupID string, appID string) error
	RemoveAllowedApps(ctx context.Context, groupID string) ([]string, error)

	// Group-centric application authorization operations
	AddAllowedGroupsForApp(ctx context.Context, appID string, groupIDs []string) error
	GetAllowedGroupsForApp(ctx context.Context, appID string) ([]string, error)
	RemoveAllAllowedGroupsForApp(ctx context.Context, appID string) ([]string, error)
}
