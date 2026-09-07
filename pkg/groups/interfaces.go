// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package groups

import (
	"context"

	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/types"
)

// Operation is an alias to kafka.Operation for permission publishing.
type Operation = kafka.Operation

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

	StreamGroupsForUser(context.Context, string, string, func(*types.Group) error) error
	StreamUsersInGroup(context.Context, string, string, func(string) error) error
}

type DatabaseInterface interface {
	ListGroups(context.Context) ([]*types.Group, error)
	CreateGroup(context.Context, *types.Group) (*types.Group, error)
	GetGroup(context.Context, string) (*types.Group, error)
	UpdateGroup(context.Context, string, *types.Group) (*types.Group, error)
	DeleteGroup(context.Context, string) error

	AddGroupOwner(context.Context, string, string) error
	ListOwnersInGroup(context.Context, string) ([]string, error)
	AddUsersToGroup(context.Context, string, []string) error
	ListUsersInGroup(context.Context, string) ([]string, error)
	RemoveUsersFromGroup(context.Context, string, []string) error

	GetGroupsForUser(context.Context, string) ([]*types.Group, error)
	UpdateGroupsForUser(context.Context, string, []string) error

	StreamGroupsForUser(context.Context, string, string, func(*types.Group) error) error
	StreamUsersInGroup(context.Context, string, string, func(string) error) error
}

type AuthorizerInterface interface {
	DeleteGroup(context.Context, string) error
}

type PermissionPublisherInterface interface {
	PublishWrite(ctx context.Context, subject, relation, object string) error
	PublishDelete(ctx context.Context, subject, relation, object string) error
	PublishOperations(ctx context.Context, ops ...Operation) error
	Close() error
}
