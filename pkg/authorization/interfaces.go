package authorization

import "context"

type ServiceInterface interface {
	GetAllowedApps(context.Context, string) ([]string, error)
	AddAllowedApp(context.Context, string, string) error
	RemoveAllowedApps(context.Context, string) error
	RemoveAllowedApp(context.Context, string, string) error

	GetAllowedGroupsForApp(context.Context, string) ([]string, error)
	RemoveAllowedGroupsForApp(context.Context, string) error
}

type AuthorizationDatabaseInterface interface {
	GetAllowedApps(context.Context, string) ([]string, error)
	AddAllowedApp(context.Context, string, string) error
	RemoveAllowedApps(context.Context, string) error
	RemoveAllowedApp(context.Context, string, string) error

	GetAllowedGroupsForApp(context.Context, string) ([]string, error)
	RemoveAllowedGroupsForApp(context.Context, string) error
}

type AuthorizerInterface interface {
	AddAllowedAppToGroup(context.Context, string, string) error
	RemoveAllowedAppFromGroup(context.Context, string, string) error

	RemoveAllowedGroupsForApp(context.Context, string) error
}
