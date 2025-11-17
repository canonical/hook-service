// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

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
	BatchCanAccess(context.Context, string, []string, []string) (bool, error)
}
