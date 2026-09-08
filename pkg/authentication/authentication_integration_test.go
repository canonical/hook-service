// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/testhelpers"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/authentication"
	"github.com/canonical/hook-service/pkg/web"
)

func TestIntegration_JWTAuthentication(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Spin up Postgres container and run migrations
	connStr := testhelpers.SetupPostgres(t)

	// 2. Spin up Ory Hydra container
	hydraEnv := testhelpers.SetupHydra(t)

	// Configure Hydra clients
	clientID, clientSecret := testhelpers.CreateHydraClient(t, hydraEnv, "Test Client")
	validToken := testhelpers.GetAccessToken(t, hydraEnv, clientID, clientSecret)

	wrongClientID, wrongClientSecret := testhelpers.CreateHydraClient(t, hydraEnv, "Wrong Subject Client")
	wrongToken := testhelpers.GetAccessToken(t, hydraEnv, wrongClientID, wrongClientSecret)

	// 3. Initialize dependencies for web.NewRouter
	tracer := tracing.NewNoopTracer()
	logger := logging.NewNoopLogger()
	monitor := monitoring.NewNoopMonitor("test", logger)

	// Create Authenticator
	jwksURL := fmt.Sprintf("%s/.well-known/jwks.json", hydraEnv.PublicURL)
	verifier, err := authentication.NewJWTAuthenticator(ctx, hydraEnv.Issuer, jwksURL, []string{clientID}, "", tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to create JWT authenticator: %v", err)
	}

	// Initialize DB Client
	dbConfig := db.Config{
		DSN:             connStr,
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		MaxReplicaLagMs: 500,
	}
	dbClient, err := db.NewDBClient(dbConfig, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to create db client: %v", err)
	}
	defer dbClient.Close()

	// Initialize Storage
	s := storage.NewStorage(dbClient, tracer, monitor, logger)
	s.SetStreamTimeout(30 * time.Second)

	// Worker Pool
	wpool := pool.NewWorkerPool(5, tracer, monitor, logger)
	defer wpool.Stop()

	// Authorizer (noop)
	authorizer := authorization.NewAuthorizer(
		openfga.NewNoopClient(tracer, monitor, logger),
		tracer,
		monitor,
		logger,
	)

	// Tenant Validator (noop)
	tenantValidator := tenants.NewNoopValidator()

	// Start router with authentication enabled
	router := web.NewRouter(
		"",   // API token
		true, // authenticationEnabled
		wpool,
		s,
		dbClient,
		authorizer,
		tenantValidator,
		verifier,
		tracer,
		monitor,
		logger,
	)

	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()
	client.Timeout = 10 * time.Second

	t.Run("Valid JWT Token Allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v0/authz/groups", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+validToken)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to execute request: %+v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Errorf("expected status OK with valid JWT, got %d: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("No JWT Token Rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v0/authz/groups", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status Unauthorized without JWT, got %d", resp.StatusCode)
		}
	})

	t.Run("Invalid JWT Token Rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v0/authz/groups", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer invalid-token-12345")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status Unauthorized with invalid JWT, got %d", resp.StatusCode)
		}
	})

	t.Run("Wrong Subject Rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v0/authz/groups", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+wrongToken)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("failed to execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status Unauthorized with wrong subject, got %d", resp.StatusCode)
		}
	})
}
