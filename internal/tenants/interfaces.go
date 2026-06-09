// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package tenants

import (
	"context"

	tenantpb "github.com/canonical/identity-platform-api/v0/tenant"
	"google.golang.org/grpc"
)

// TenantServiceClientInterface is the narrow tenant-service dependency used by the validator.
type TenantServiceClientInterface interface {
	LookupTenants(ctx context.Context, in *tenantpb.LookupTenantsRequest, opts ...grpc.CallOption) (*tenantpb.LookupTenantsResponse, error)
}

// TenantValidatorInterface validates that a user is an active member of a tenant.
type TenantValidatorInterface interface {
	// ValidateMembership checks if the user identified by identityID is an
	// active member of the given tenant. Returns nil if valid, ErrNotMember
	// if not, or an error on failure.
	ValidateMembership(ctx context.Context, identityID, tenantID string) error
}
