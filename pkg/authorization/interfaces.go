// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import "context"

type ServiceInterface interface {
	GetAllowedAppsInGroup(context.Context, string) ([]string, error)
	AddAllowedAppToGroup(context.Context, string, string) error
	RemoveAllAllowedAppsFromGroup(context.Context, string) error
	RemoveAllowedAppFromGroup(context.Context, string, string) error

	GetAllowedGroupsForApp(context.Context, string) ([]string, error)
	RemoveAllAllowedGroupsForApp(context.Context, string) error
}

type AuthorizationDatabaseInterface interface {
	GetAllowedApps(context.Context, string) ([]string, error)
	AddAllowedApp(context.Context, string, string) error
	AddAllowedApps(context.Context, string, []string) error
	RemoveAllowedApp(context.Context, string, string) error
	RemoveAllowedApps(context.Context, string) ([]string, error)

	AddAllowedGroupsForApp(context.Context, string, []string) error
	GetAllowedGroupsForApp(context.Context, string) ([]string, error)
	RemoveAllAllowedGroupsForApp(context.Context, string) ([]string, error)
}

type AuthorizerInterface interface {
	AddAllowedAppToGroup(context.Context, string, string) error
	RemoveAllowedAppFromGroup(context.Context, string, string) error
	RemoveAllAllowedAppsFromGroup(context.Context, string) error

	RemoveAllAllowedGroupsForApp(context.Context, string) error
}
