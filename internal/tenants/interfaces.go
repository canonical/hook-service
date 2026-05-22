// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package tenants

import "context"

// TenantValidatorInterface validates that a user is an active member of a tenant.
type TenantValidatorInterface interface {
	// ValidateMembership checks if the user identified by identityID is an
	// active member of the given tenant. Returns nil if valid, ErrNotMember
	// if not, or an error on failure.
	ValidateMembership(ctx context.Context, identityID, tenantID string) error
}
