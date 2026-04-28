// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
	"github.com/ory/hydra/v2/oauth2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// HookContext contains the enriched result of processing an OAuth token hook
// request. It is returned by ProcessRequest on success.
type HookContext struct {
	// Groups is the list of groups the user belongs to.
	Groups []*types.Group
	// TenantID is the tenant the request is scoped to, or empty if none.
	TenantID string
}

// ErrTooBusy is returned by ProcessRequest when the worker pool queue is full.
var ErrTooBusy = errors.New("worker pool is full")

// errTenantInternal is returned by ProcessRequest when tenant membership
// validation fails for reasons other than the user not being a member (e.g.
// the tenant service is unreachable).
var errTenantInternal = errors.New("tenant service internal error")

// groupFetchResult carries the outcome of a pool-dispatched FetchUserGroups call.
type groupFetchResult struct {
	groups []*types.Group
	err    error
}

// tenantValidateResult carries the outcome of a pool-dispatched ValidateMembership call.
type tenantValidateResult struct {
	err error
}

type Service struct {
	clients         []ClientInterface
	authz           AuthorizerInterface
	tenantValidator TenantValidatorInterface
	wpool           pool.WorkerPoolInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// ProcessRequest orchestrates an OAuth token hook request. FetchUserGroups and
// (when a tenant is present) ValidateMembership are dispatched to the worker
// pool concurrently. AuthorizeRequest is gated only on FetchUserGroups, so
// tenant validation proceeds in parallel with authorization. Returns ErrTooBusy
// when the pool queue is full; all other errors indicate an authorization failure.
func (s *Service) ProcessRequest(ctx context.Context, user User, req oauth2.TokenHookRequest) (*HookContext, error) {
	ctx, span := s.tracer.Start(ctx, "hooks.Service.ProcessRequest")
	defer span.End()

	tenantID := extractTenantID(&req)

	groupsCh := make(chan *pool.Result[any], 1)
	tenantCh := make(chan *pool.Result[any], 1)
	var groupsWg, tenantWg sync.WaitGroup

	var (
		tenantOnce sync.Once
		tenantErr  error
	)
	// waitTenant drains the in-flight tenant validation job and stores its
	// error in tenantErr. Idempotent via sync.Once; safe when tenantID is empty.
	// Deferred below so error-path returns drain automatically without explicit calls.
	waitTenant := func() {
		tenantOnce.Do(func() {
			if tenantID == "" {
				return
			}
			tenantWg.Wait()
			close(tenantCh)
			if r, ok := <-tenantCh; ok {
				tenantErr = r.Value.(tenantValidateResult).err
			}
		})
	}
	defer waitTenant()

	groupsWg.Add(1)
	if _, err := s.wpool.Submit(func() any {
		groups, err := s.FetchUserGroups(ctx, user)
		return groupFetchResult{groups: groups, err: err}
	}, groupsCh, &groupsWg); err != nil {
		groupsWg.Done()
		return nil, ErrTooBusy
	}

	if tenantID != "" {
		tenantWg.Add(1)
		if _, err := s.wpool.Submit(func() any {
			return tenantValidateResult{err: s.tenantValidator.ValidateMembership(ctx, user.SubjectId, tenantID)}
		}, tenantCh, &tenantWg); err != nil {
			tenantWg.Done()
			groupsWg.Wait()
			close(groupsCh)
			return nil, ErrTooBusy
		}
	}

	// Wait for groups only: AuthorizeRequest depends on them, not on tenant validation.
	groupsWg.Wait()
	close(groupsCh)

	r, ok := <-groupsCh
	if !ok {
		return nil, ErrTooBusy
	}
	gResult := r.Value.(groupFetchResult)
	if gResult.err != nil {
		return nil, fmt.Errorf("cannot fetch user groups: %v", gResult.err)
	}

	span.SetAttributes(attribute.Int("groups.count", len(gResult.groups)))

	// AuthorizeRequest runs while tenant validation may still be in flight.
	allowed, err := s.AuthorizeRequest(ctx, user, req, gResult.groups)
	if err != nil {
		return nil, fmt.Errorf("cannot authorize request: %v", err)
	}

	if !allowed {
		return nil, fmt.Errorf("access denied for user %s to client %s", user.GetUserId(), req.Request.ClientID)
	}

	// Explicitly wait here so we can inspect tenantErr before returning.
	// The deferred call is a no-op after this point.
	waitTenant()
	if tenantErr != nil {
		if errors.Is(tenantErr, tenants.ErrNotMember) {
			return nil, fmt.Errorf("user %s is not a member of tenant %s: %w", user.SubjectId, tenantID, tenants.ErrNotMember)
		}
		return nil, fmt.Errorf("cannot validate tenant membership: %w", errTenantInternal)
	}

	return &HookContext{
		Groups:   gResult.groups,
		TenantID: tenantID,
	}, nil
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
	} else if len(req.Request.GrantedAudience) > 0 {
		allowed, err = s.authz.BatchCanAccess(ctx, user.GetUserId(), req.Request.GrantedAudience, groupIDs)
		span.SetAttributes(
			attribute.String("authorization.type", "batch_access"),
			attribute.StringSlice("granted_audience", req.Request.GrantedAudience),
		)
	} else {
		s.logger.Debugf("Allowed, because empty granted audience in request for service account: %#v", user)
		allowed = true
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

// extractTenantID returns the tenant ID from the session extra data, or
// an empty string if none was set at login time.
func extractTenantID(req *oauth2.TokenHookRequest) string {
	if req.Session == nil || req.Session.Extra == nil {
		return ""
	}
	tid, _ := req.Session.Extra["_tenant_id"].(string)
	return tid
}

func NewService(
	clients []ClientInterface,
	authz AuthorizerInterface,
	tenantValidator TenantValidatorInterface,
	wpool pool.WorkerPoolInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.clients = clients
	s.authz = authz
	s.tenantValidator = tenantValidator
	s.wpool = wpool

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}
