// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"context"
	"errors"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	// ErrInvalidToken indicates the token could not be verified or is malformed/expired.
	ErrInvalidToken = errors.New("invalid token")
	// ErrUnauthorized indicates the token is valid but fails required subject or scope criteria.
	ErrUnauthorized = errors.New("unauthorized")
)

// Claims represents the standard and custom JWT claims extracted during token verification.
type Claims struct {
	Subject string   `json:"sub"`
	Scope   string   `json:"scope"`
	Scopes  []string `json:"scp"`
}

// ProviderInterface retrieves an IDTokenVerifier for an OIDC issuer.
type ProviderInterface interface {
	// Verifier returns the token verifier associated with the specified OIDC issuer.
	Verifier(*oidc.Config) *oidc.IDTokenVerifier
}

// TokenVerifierInterface defines token verification and authorization operations.
type TokenVerifierInterface interface {
	// VerifyToken verifies a raw JWT string and validates authorization claims.
	// Returns the extracted claims if valid and authorized, or an error.
	VerifyToken(ctx context.Context, rawToken string) (*Claims, error)
}
