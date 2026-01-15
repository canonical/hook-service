// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"

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

func (v *JWTVerifier) VerifyToken(ctx context.Context, rawToken string) (*oidc.IDToken, error) {
	ctx, span := v.tracer.Start(ctx, "authentication.JWTVerifier.VerifyToken")
	defer span.End()

	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, err
	}

	return token, nil
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
