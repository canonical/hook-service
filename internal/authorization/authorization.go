// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import (
	"context"
	"fmt"
	"slices"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/tracing"
)

var ErrInvalidAuthModel = fmt.Errorf("Invalid authorization model schema")

type Authorizer struct {
	client AuthzClientInterface

	wpool pool.WorkerPoolInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (a *Authorizer) Check(ctx context.Context, user string, relation string, object string, contextualTuples ...openfga.Tuple) (bool, error) {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.Check")
	defer span.End()

	return a.client.Check(ctx, user, relation, object, contextualTuples...)
}

func (a *Authorizer) ListObjects(ctx context.Context, user string, relation string, objectType string) ([]string, error) {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.ListObjects")
	defer span.End()

	return a.client.ListObjects(ctx, user, relation, objectType)
}

func (a *Authorizer) FilterObjects(ctx context.Context, user string, relation string, objectType string, objs []string) ([]string, error) {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.FilterObjects")
	defer span.End()

	allowedObjs, err := a.ListObjects(ctx, user, relation, objectType)
	if err != nil {
		return nil, err
	}

	var ret []string
	for _, obj := range allowedObjs {
		if slices.Contains(objs, obj) {
			ret = append(ret, obj)
		}
	}
	return ret, nil
}

func (a *Authorizer) ValidateModel(ctx context.Context) error {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.ValidateModel")
	defer span.End()

	v0AuthzModel := NewAuthorizationModelProvider("v0")
	model := *v0AuthzModel.GetModel()

	eq, err := a.client.CompareModel(ctx, model)
	if err != nil {
		return err
	}
	if !eq {
		return ErrInvalidAuthModel
	}
	return nil
}

func (a *Authorizer) CanAccess(ctx context.Context, userId, clientId string, groups []string) (bool, error) {
	ctxTuples := []openfga.Tuple{}
	for _, group := range groups {
		ctxTuples = append(ctxTuples, *openfga.NewTuple(UserTuple(userId), MEMBER_RELATION, GroupTuple(group)))
	}
	return a.Check(ctx, UserTuple(userId), CAN_ACCESS_RELATION, ClientTuple(clientId), ctxTuples...)
}

func NewAuthorizer(client AuthzClientInterface, wpool pool.WorkerPoolInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Authorizer {
	authorizer := new(Authorizer)
	authorizer.client = client
	authorizer.wpool = wpool
	authorizer.tracer = tracer
	authorizer.monitor = monitor
	authorizer.logger = logger

	return authorizer
}
