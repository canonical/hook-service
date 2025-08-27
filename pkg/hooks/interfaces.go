package hooks

import (
	"context"

	"github.com/ory/hydra/v2/oauth2"
)

type ServiceInterface interface {
	FetchUserGroups(context.Context, User) ([]string, error)
	AuthorizeRequest(context.Context, User, oauth2.TokenHookRequest, []string) (bool, error)
}

type ClientInterface interface {
	FetchUserGroups(context.Context, User) ([]string, error)
}

type AuthorizerInterface interface {
	CanAccess(context.Context, string, string, []string) (bool, error)
}
