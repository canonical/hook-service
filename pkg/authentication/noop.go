// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"context"
)

// NoopVerifier is a no-op implementation of TokenVerifierInterface.
type NoopVerifier struct{}

// NewNoopVerifier returns a no-op token verifier that allows all requests.
func NewNoopVerifier() *NoopVerifier {
	return &NoopVerifier{}
}

// VerifyToken always returns an empty Claims pointer and nil error (allowing all requests).
func (n *NoopVerifier) VerifyToken(ctx context.Context, rawIDToken string) (*Claims, error) {
	return &Claims{}, nil
}
