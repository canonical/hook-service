package groups

import (
	"context"
)

type ServiceInterface interface {
	ListGroups(context.Context) ([]*Group, error)
	CreateGroup(context.Context, string, string, string, groupType) (*Group, error)
	GetGroup(context.Context, string) (*Group, error)
	UpdateGroup(context.Context, string, *Group) (*Group, error)
	DeleteGroup(context.Context, string) error

	AddUsersToGroup(context.Context, string, []string) error
	ListUsersInGroup(context.Context, string) ([]string, error)
	RemoveUsersFromGroup(context.Context, string, []string) error
	RemoveAllUsersFromGroup(context.Context, string) error

	GetGroupsForUser(context.Context, string) ([]*Group, error)
	UpdateGroupsForUser(context.Context, string, []string) error
	RemoveGroupsForUser(context.Context, string) error
}

type DatabaseInterface interface {
	ListGroups(context.Context) ([]*Group, error)
	CreateGroup(context.Context, *Group) (*Group, error)
	GetGroup(context.Context, string) (*Group, error)
	UpdateGroup(context.Context, string, *Group) (*Group, error)
	DeleteGroup(context.Context, string) error

	AddUsersToGroup(context.Context, string, []string) error
	ListUsersInGroup(context.Context, string) ([]string, error)
	RemoveUsersFromGroup(context.Context, string, []string) error
	RemoveAllUsersFromGroup(context.Context, string) ([]string, error)

	GetGroupsForUser(context.Context, string) ([]*Group, error)
	UpdateGroupsForUser(context.Context, string, []string) error
	RemoveGroupsForUser(context.Context, string) ([]string, error)
}

type AuthorizerInterface interface {
	DeleteGroup(context.Context, string) error
}
