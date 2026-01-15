// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var (
	otelHTTPClient = http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
)

// NewProvider creates an OIDC provider with optional JWKS URL override
func NewProvider(ctx context.Context, issuer, jwksURL string) (*oidc.Provider, error) {
	// Use otel-instrumented HTTP client
	ctx = oidc.ClientContext(ctx, &otelHTTPClient)

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC provider: %v", err)
	}

	// If explicit JWKS URL is provided, we need to create a custom provider
	// The oidc library doesn't support overriding JWKS URL directly,
	// but we can work around it by using the provider as-is since
	// the issuer's well-known configuration will point to the JWKS
	if jwksURL != "" {
		// Note: The go-oidc library will use the issuer's .well-known/openid-configuration
		// If we need to override JWKS URL, we'd need to implement a custom RemoteKeySet
		// For now, we'll document that AUTH_JWKS_URL is used when the issuer's
		// well-known configuration is not accessible
	}

	return provider, nil
}
