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
	"github.com/canonical/hook-service/internal/tracing"
)

var ErrInvalidAuthModel = fmt.Errorf("invalid authorization model schema")

type Authorizer struct {
	client AuthzClientInterface

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

func (a *Authorizer) BatchCanAccess(ctx context.Context, userId string, clientIds []string, groups []string) (bool, error) {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.BatchCanAccess")
	defer span.End()

	ctxTuples := []openfga.Tuple{}
	for _, group := range groups {
		ctxTuples = append(ctxTuples, *openfga.NewTuple(UserTuple(userId), MEMBER_RELATION, GroupTuple(group)))
	}

	tuples := []openfga.TupleWithContext{}
	for _, clientId := range clientIds {
		tuples = append(tuples, *openfga.NewTupleWithContext(UserTuple(userId), CAN_ACCESS_RELATION, ClientTuple(clientId), ctxTuples))
	}

	return a.client.BatchCheck(ctx, tuples...)
}

func (a *Authorizer) AddAllowedAppToGroup(ctx context.Context, groupID, clientID string) error {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.AddAllowedAppToGroup")
	defer span.End()

	return a.client.WriteTuple(ctx, GroupMemberTuple(groupID), CAN_ACCESS_RELATION, ClientTuple(clientID))
}

func (a *Authorizer) RemoveAllowedAppFromGroup(ctx context.Context, groupID, clientID string) error {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.RemoveAllowedAppFromGroup")
	defer span.End()

	return a.client.DeleteTuple(ctx, GroupMemberTuple(groupID), CAN_ACCESS_RELATION, ClientTuple(clientID))
}

func (a *Authorizer) RemoveAllAllowedGroupsForApp(ctx context.Context, clientID string) error {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.RemoveAllAllowedGroupsForApp")
	defer span.End()

	cToken := ""
	for {
		r, err := a.client.ReadTuples(ctx, GroupMemberTuple(""), CAN_ACCESS_RELATION, ClientTuple(clientID), cToken)
		if err != nil {
			a.logger.Errorf("error when retrieving tuples: %s", err)
			return err
		}
		if len(r.Tuples) == 0 {
			break
		}
		ts := make([]openfga.Tuple, len(r.Tuples))
		for i, t := range r.Tuples {
			ts[i] = *openfga.NewTuple(t.Key.User, t.Key.Relation, t.Key.Object)
		}
		if err := a.client.DeleteTuples(ctx, ts...); err != nil {
			a.logger.Errorf("error when deleting tuples %v: %s", ts, err)
			return err
		}
		if r.ContinuationToken == "" {
			break
		}
		cToken = r.ContinuationToken
	}
	return nil
}

func (a *Authorizer) RemoveAllAllowedAppsFromGroup(ctx context.Context, groupId string) error {
	ctx, span := a.tracer.Start(ctx, "authorization.Authorizer.RemoveAllAllowedAppsFromGroup")
	defer span.End()

	cToken := ""
	for {
		r, err := a.client.ReadTuples(ctx, GroupMemberTuple(groupId), CAN_ACCESS_RELATION, ClientTuple(""), cToken)
		if err != nil {
			a.logger.Errorf("error when retrieving tuples: %s", err)
			return err
		}
		if len(r.Tuples) == 0 {
			break
		}
		ts := make([]openfga.Tuple, len(r.Tuples))
		for i, t := range r.Tuples {
			ts[i] = *openfga.NewTuple(t.Key.User, t.Key.Relation, t.Key.Object)
		}
		if err := a.client.DeleteTuples(ctx, ts...); err != nil {
			a.logger.Errorf("error when deleting tuples %v: %s", ts, err)
			return err
		}
		if r.ContinuationToken == "" {
			break
		}
		cToken = r.ContinuationToken
	}
	return nil
}

func NewAuthorizer(client AuthzClientInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Authorizer {
	authorizer := new(Authorizer)
	authorizer.client = client
	authorizer.tracer = tracer
	authorizer.monitor = monitor
	authorizer.logger = logger

	return authorizer
}
