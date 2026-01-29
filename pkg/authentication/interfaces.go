// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
)

type ProviderInterface interface {
	// Verifier returns the token verifier associated with the specified OIDC issuer
	Verifier(*oidc.Config) *oidc.IDTokenVerifier
}

type TokenVerifierInterface interface {
	// VerifyToken verifies a raw JWT string and validates authorization claims
	// Returns true if the token is valid and authorized, false otherwise
	VerifyToken(ctx context.Context, rawToken string) (bool, error)
}
