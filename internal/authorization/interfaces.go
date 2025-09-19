// Copyright 2024 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import (
	"context"

	fga "github.com/openfga/go-sdk"

	"github.com/canonical/hook-service/internal/openfga"
)

type AuthorizerInterface interface {
	ListObjects(context.Context, string, string, string) ([]string, error)
	Check(context.Context, string, string, string, ...openfga.Tuple) (bool, error)
	FilterObjects(context.Context, string, string, string, []string) ([]string, error)
	ValidateModel(context.Context) error
	CanAccess(context.Context, string, string, []string) (bool, error)
	BatchCanAccess(context.Context, string, []string, []string) (bool, error)
}

type AuthzClientInterface interface {
	ListObjects(context.Context, string, string, string) ([]string, error)
	Check(context.Context, string, string, string, ...openfga.Tuple) (bool, error)
	BatchCheck(context.Context, ...openfga.TupleWithContext) (bool, error)
	ReadModel(context.Context) (*fga.AuthorizationModel, error)
	CompareModel(context.Context, fga.AuthorizationModel) (bool, error)
	WriteTuple(ctx context.Context, user, relation, object string) error
	DeleteTuple(ctx context.Context, user, relation, object string) error
}
