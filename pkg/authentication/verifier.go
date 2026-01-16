// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type JWTVerifier struct {
	verifier *oidc.IDTokenVerifier

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (v *JWTVerifier) VerifyToken(ctx context.Context, rawToken string, allowedSubjects []string, requiredScope string) (bool, error) {
	ctx, span := v.tracer.Start(ctx, "authentication.JWTVerifier.VerifyToken")
	defer span.End()

	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return false, err
	}

	var claims struct {
		Subject string   `json:"sub"`
		Scope   string   `json:"scope"`
		Scopes  []string `json:"scp"`
	}

	if err := token.Claims(&claims); err != nil {
		v.logger.Debugf("Failed to extract claims: %v", err)
		return false, err
	}

	if len(allowedSubjects) > 0 {
		for _, allowedSub := range allowedSubjects {
			if claims.Subject == allowedSub {
				return true, nil
			}
		}
	}

	if requiredScope != "" {
		if claims.Scope != "" {
			scopes := strings.Fields(claims.Scope)
			for _, scope := range scopes {
				if scope == requiredScope {
					return true, nil
				}
			}
		}

		for _, scope := range claims.Scopes {
			if scope == requiredScope {
				return true, nil
			}
		}
	}

	if len(allowedSubjects) == 0 && requiredScope == "" {
		v.logger.Debugf("No authorization criteria configured")
		v.logger.Security().AuthzFailure(claims.Subject, "jwt_api_access")
		return false, nil
	}

	v.logger.Security().AuthzFailure(claims.Subject, "jwt_api_access")
	return false, nil
}

func NewJWTVerifier(provider ProviderInterface, issuer string, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *JWTVerifier {
	v := &JWTVerifier{
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}

	// Create verifier config - skip audience validation as per requirements
	config := &oidc.Config{
		SkipClientIDCheck: true,
		SkipIssuerCheck:   false,
	}

	v.verifier = provider.Verifier(config)

	return v
}

// NewJWTVerifierDirect creates a JWT verifier with a pre-configured IDTokenVerifier
// This is used when JWKS URL is provided manually instead of OIDC discovery
func NewJWTVerifierDirect(verifier *oidc.IDTokenVerifier, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *JWTVerifier {
	return &JWTVerifier{
		verifier: verifier,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
