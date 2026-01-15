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

// SetupJWTAuthentication initializes JWT authentication based on configuration
// Returns the middleware if successful, or nil if authentication should be disabled
func SetupJWTAuthentication(
	ctx context.Context,
	enabled bool,
	issuer string,
	jwksURL string,
	allowedSubjects string,
	requiredScope string,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) (*Middleware, error) {
	if !enabled {
		logger.Info("JWT authentication is disabled")
		return nil, nil
	}

	if issuer == "" {
		return nil, fmt.Errorf("AUTH_ENABLED is true but AUTH_ISSUER is not configured")
	}

	authConfig := NewConfig(enabled, issuer, allowedSubjects, requiredScope)

	var verifier *JWTVerifier

	if jwksURL != "" {
		// Use manual JWKS URL
		logger.Infof("Using manual JWKS URL: %s", jwksURL)
		_, idTokenVerifier, err := NewProviderWithJWKS(ctx, issuer, jwksURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWKS verifier: %v", err)
		}
		verifier = NewJWTVerifierDirect(idTokenVerifier, tracer, monitor, logger)
		logger.Info("JWT authentication is enabled with manual JWKS URL")
	} else {
		// Use OIDC discovery
		logger.Infof("Using OIDC discovery for issuer: %s", issuer)
		provider, err := NewProvider(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("failed to create OIDC provider: %v", err)
		}
		verifier = NewJWTVerifier(provider, issuer, tracer, monitor, logger)
		logger.Info("JWT authentication is enabled with OIDC discovery")
	}

	middleware := NewMiddleware(authConfig, verifier, tracer, monitor, logger)
	return middleware, nil
}
