// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

// NewJWTAuthenticator initializes a JWT token verifier.
func NewJWTAuthenticator(
	ctx context.Context,
	issuer string,
	jwksURL string,
	allowedSubjects []string,
	requiredScope string,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) (TokenVerifierInterface, error) {
	if issuer == "" {
		return nil, fmt.Errorf("issuer is required for JWT authentication")
	}

	var verifier *JWTVerifier

	if jwksURL != "" {
		logger.Infof("Using manual JWKS URL: %s", jwksURL)
		_, idTokenVerifier, err := NewProviderWithJWKS(ctx, issuer, jwksURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWKS verifier: %v", err)
		}
		verifier = NewJWTVerifierDirect(idTokenVerifier, allowedSubjects, requiredScope, tracer, monitor, logger)
		logger.Info("JWT authentication is enabled with manual JWKS URL")
	} else {
		logger.Infof("Using OIDC discovery for issuer: %s", issuer)
		provider, err := NewProvider(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("failed to create OIDC provider: %v", err)
		}
		verifier = NewJWTVerifier(provider, issuer, allowedSubjects, requiredScope, tracer, monitor, logger)
		logger.Info("JWT authentication is enabled with OIDC discovery")
	}

	return verifier, nil
}
