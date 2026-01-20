// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
	"github.com/ory/hydra/v2/oauth2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Service struct {
	clients []ClientInterface
	authz   AuthorizerInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) FetchUserGroups(ctx context.Context, user User) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "hooks.Service.FetchUserGroups")
	defer span.End()

	span.SetAttributes(
		attribute.String("user.id", user.GetUserId()),
		attribute.Int("clients.count", len(s.clients)),
	)

	ret := make([]*types.Group, 0)

	for _, c := range s.clients {
		// TODO: Generate go routines to run this in parallel
		groups, err := c.FetchUserGroups(ctx, user)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to fetch user groups from client")
			return nil, err
		}
		ret = append(ret, groups...)
	}

	span.SetAttributes(attribute.Int("groups.total_count", len(ret)))
	span.SetStatus(codes.Ok, "user groups fetched successfully")

	return ret, nil
}

// This implements deny by default
// TODO: we should make this configurable
func (s *Service) AuthorizeRequest(
	ctx context.Context,
	user User,
	req oauth2.TokenHookRequest,
	groups []*types.Group,
) (bool, error) {
	ctx, span := s.tracer.Start(ctx, "hooks.Service.AuthorizeRequest")
	defer span.End()

	groupIDs := make([]string, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}

	isServiceAcct := isServiceAccount(req.Request.GrantTypes)
	span.SetAttributes(
		attribute.String("user.id", user.GetUserId()),
		attribute.String("client.id", req.Request.ClientID),
		attribute.Int("groups.count", len(groupIDs)),
		attribute.Bool("is_service_account", isServiceAcct),
	)

	var allowed bool
	var err error

	if !isServiceAcct {
		allowed, err = s.authz.CanAccess(ctx, user.GetUserId(), req.Request.ClientID, groupIDs)
		span.SetAttributes(attribute.String("authorization.type", "user_access"))
	} else {
		allowed, err = s.authz.BatchCanAccess(ctx, user.GetUserId(), req.Request.GrantedAudience, groupIDs)
		span.SetAttributes(
			attribute.String("authorization.type", "batch_access"),
			attribute.StringSlice("granted_audience", req.Request.GrantedAudience),
		)
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "authorization check failed")
		return false, err
	}

	span.SetAttributes(attribute.Bool("authorization.allowed", allowed))
	if allowed {
		span.SetStatus(codes.Ok, "authorization successful")
	} else {
		span.SetStatus(codes.Ok, "authorization denied")
	}

	return allowed, nil
}

func NewService(
	clients []ClientInterface,
	authz AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.clients = clients
	s.authz = authz

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}
