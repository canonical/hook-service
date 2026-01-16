// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
)

type NoopVerifier struct{}

// NewNoopVerifier returns a no-op token verifier that allows all requests.
func NewNoopVerifier() *NoopVerifier {
	return &NoopVerifier{}
}

// VerifyToken always returns true, nil (allowing all requests).
func (n *NoopVerifier) VerifyToken(ctx context.Context, rawIDToken string, allowedSubjects []string, requiredScope string) (bool, error) {
	return true, nil
}
