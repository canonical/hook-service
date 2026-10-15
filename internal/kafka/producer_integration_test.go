// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/canonical/hook-service/internal/kafka/sasl"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring/prometheus"
	"github.com/canonical/hook-service/internal/testhelpers"
	"github.com/canonical/hook-service/internal/tracing"
)

func TestIntegration_Hydra_OAuthBearerTokenSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := t.Context()

	hydraEnv := testhelpers.SetupHydra(t)
	clientID, clientSecret := testhelpers.CreateHydraClient(t, hydraEnv, "oauthbearer-integration-client", "kafka")

	tokenURL := fmt.Sprintf("%s/oauth2/token", hydraEnv.PublicURL)
	testLogger := logging.NewLogger("debug")
	testMonitor := prometheus.NewMonitor("test-kafka-producer", testLogger)
	testTracer := tracing.NewNoopTracer()
	ts := sasl.NewTokenSource(sasl.TokenSourceConfig{
		TokenURL:      tokenURL,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		Audience:      "kafka",
		RefreshMargin: 60 * time.Second,
		FetchTimeout:  10 * time.Second,
	}, testTracer, testMonitor, testLogger)

	// 1. Initial token acquisition from live Hydra
	tok, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("failed to fetch token from Hydra: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty access token from Hydra")
	}

	// 2. Token caching verification
	tok2, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("failed to fetch cached token: %v", err)
	}
	if tok2 != tok {
		t.Errorf("cached access token changed: got %q, want %q", tok2, tok)
	}
}
