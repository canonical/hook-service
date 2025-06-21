package hooks

import (
	"context"
)

type ServiceInterface interface {
	FetchUserGroups(context.Context, User) ([]string, error)
}

type ClientInterface interface {
	FetchUserGroups(context.Context, User) ([]string, error)
}
