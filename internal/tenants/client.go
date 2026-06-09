// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package tenants

import (
	"context"
	"errors"
	"fmt"
	"time"

	tenantpb "github.com/canonical/identity-platform-api/v0/tenant"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ErrNotMember indicates the user is not an active member of the tenant.
var ErrNotMember = errors.New("user is not a member of the tenant")

// Client calls tenant-service's LookupTenants RPC to validate membership.
type Client struct {
	grpcClient TenantServiceClientInterface
	timeout    time.Duration

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// NewClient creates a tenant-service client backed by the generated gRPC client.
// timeout caps the total time allowed for each lookup request.
func NewClient(grpcClient TenantServiceClientInterface, timeout time.Duration, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Client {
	return &Client{
		grpcClient: grpcClient,
		timeout:    timeout,
		tracer:     tracer,
		monitor:    monitor,
		logger:     logger,
	}
}

// ValidateMembership checks whether the user identified by identityID is an
// active member of the given tenant. Returns nil if valid, ErrNotMember if
// the user has no active membership, or an error on lookup failure.
func (c *Client) ValidateMembership(ctx context.Context, identityID, tenantID string) error {
	ctx, span := c.tracer.Start(ctx, "tenants.Client.ValidateMembership")
	defer span.End()

	span.SetAttributes(
		attribute.String("identity_id", identityID),
		attribute.String("tenant_id", tenantID),
	)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.grpcClient.LookupTenants(ctx, &tenantpb.LookupTenantsRequest{IdentityId: identityID})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "cannot look up tenants")
		return fmt.Errorf("cannot look up tenants: %v", err)
	}

	if resp == nil {
		err := errors.New("empty response")
		span.RecordError(err)
		span.SetStatus(codes.Error, "empty tenant-service response")
		return fmt.Errorf("cannot look up tenants: %v", err)
	}

	for _, tenant := range resp.GetTenants() {
		if tenant.GetId() == tenantID {
			span.SetStatus(codes.Ok, "membership validated")
			return nil
		}
	}

	span.SetStatus(codes.Ok, "membership denied")
	return ErrNotMember
}
