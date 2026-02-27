// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package importer

import "context"

// UserGroupMapping represents a mapping between a user identifier and a group name.
type UserGroupMapping struct {
	UserID    string
	GroupName string
}

// DriverInterface defines the contract for external data sources that can
// provide user-to-group mappings for import into the local database.
type DriverInterface interface {
	Prefix() string
	FetchAllUserGroups(ctx context.Context) ([]UserGroupMapping, error)
}
