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

// NewProvider creates an OIDC provider using the issuer's well-known configuration
func NewProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	// Use otel-instrumented HTTP client
	ctx = oidc.ClientContext(ctx, &otelHTTPClient)

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC provider: %v", err)
	}

	return provider, nil
}
