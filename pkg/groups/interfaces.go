// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"

	"github.com/canonical/hook-service/internal/types"
)

type ServiceInterface interface {
	ListGroups(context.Context) ([]*types.Group, error)
	CreateGroup(context.Context, *types.Group) (*types.Group, error)
	GetGroup(context.Context, string) (*types.Group, error)
	UpdateGroup(context.Context, string, *types.Group) (*types.Group, error)
	DeleteGroup(context.Context, string) error

	AddUsersToGroup(context.Context, string, []string) error
	ListUsersInGroup(context.Context, string) ([]string, error)
	RemoveUsersFromGroup(context.Context, string, []string) error

	GetGroupsForUser(context.Context, string) ([]*types.Group, error)
	UpdateGroupsForUser(context.Context, string, []string) error

	ImportUserGroupsFromSalesforce(context.Context, SalesforceClientInterface) (int, error)
}

type DatabaseInterface interface {
	ListGroups(context.Context) ([]*types.Group, error)
	CreateGroup(context.Context, *types.Group) (*types.Group, error)
	GetGroup(context.Context, string) (*types.Group, error)
	UpdateGroup(context.Context, string, *types.Group) (*types.Group, error)
	DeleteGroup(context.Context, string) error

	AddUsersToGroup(context.Context, string, []string) error
	ListUsersInGroup(context.Context, string) ([]string, error)
	RemoveUsersFromGroup(context.Context, string, []string) error

	GetGroupsForUser(context.Context, string) ([]*types.Group, error)
	UpdateGroupsForUser(context.Context, string, []string) error
}

type AuthorizerInterface interface {
	DeleteGroup(context.Context, string) error
}

type SalesforceClientInterface interface {
	Query(string, any) error
}

type SalesforceTeamMember struct {
	Email      string `mapstructure:"fHCM2__Email__c"`
	Department string `mapstructure:"Department2__c"`
	Team       string `mapstructure:"fHCM2__Team__c"`
}
