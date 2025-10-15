package groups

import "context"

type ServiceInterface interface {
	ListGroups(context.Context) ([]*Group, error)
	GetGroup(context.Context, string) (*Group, error)
	GetGroupByName(context.Context, string) (*Group, error)
	CreateGroup(context.Context, string) (*Group, error)
	DeleteGroup(context.Context, string) error

	ListGroupMembers(context.Context, string) ([]string, error)
	AddGroupMember(context.Context, string, string) error
	RemoveGroupMember(context.Context, string, string) error

	ListUserGroups(context.Context, string) ([]*Group, error)
}

type AuthorizationServiceInterface interface {
	RemoveAllowedApps(context.Context, string) error
}

type GroupDatabaseInterface interface {
	ListGroups(context.Context) ([]*Group, error)
	GetGroup(context.Context, string) (*Group, error)
	GetGroupByName(context.Context, string) (*Group, error)
	CreateGroup(context.Context, string, string) (*Group, error)
	DeleteGroup(context.Context, string) error

	ListGroupMembers(context.Context, string) ([]string, error)
	AddGroupMember(context.Context, string, string) error
	RemoveGroupMember(context.Context, string, string) error

	ListUserGroups(context.Context, string) ([]*Group, error)
}
