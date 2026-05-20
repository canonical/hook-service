// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package tenants

import "context"

// NoopValidator is used when tenant validation is disabled
// (TENANT_SERVICE_URL is empty). All calls succeed.
type NoopValidator struct{}

// NewNoopValidator returns a validator that always succeeds.
func NewNoopValidator() *NoopValidator {
	return &NoopValidator{}
}

// ValidateMembership always returns nil (no validation).
func (n *NoopValidator) ValidateMembership(_ context.Context, _, _ string) error {
	return nil
}
