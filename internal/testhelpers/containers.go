// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

// Package testhelpers provides shared testcontainers-based fixtures for
// integration tests. Helpers start containers, register teardown via
// t.Cleanup, and fail hard via t.Fatalf on any error. A container runtime
// (Docker or Podman socket) is a documented prerequisite for running
// integration tests; use `go test -short` for the unit-only suite.
//
// This package imports internal/openfga and internal/authorization, so those
// packages cannot use these helpers from in-package (non-_test) tests
// without creating an import cycle; this leaf positioning is intentional.
package testhelpers

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/openfga/go-sdk/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	internalAuthz "github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/tracing"
)

// Container image versions used across all integration tests. Bump here once.
const (
	postgresImage = "postgres:16-alpine"
	hydraImage    = "oryd/hydra:v25.4.0"
	openfgaImage  = "openfga/openfga:v1.10.0"
)

const (
	// openFGAPresharedKey is the preshared API token the test OpenFGA
	// container is configured with and clients authenticate against.
	openFGAPresharedKey = "42"

	// hydraIssuer is the issuer URL the test Hydra container is configured
	// with (URLS_SELF_ISSUER). Clients reach Hydra through a mapped host
	// port, but the issuer claim in issued tokens stays this loopback URL.
	hydraIssuer = "http://127.0.0.1:4444/"

	pingTimeout   = 30 * time.Second
	pingRetryWait = time.Second
)

// HydraEnv carries the mapped endpoints of a started Ory Hydra container.
// Issuer is the URL Hydra is configured with (URLS_SELF_ISSUER); it is the
// issuer claim tokens will carry, not the mapped public address.
type HydraEnv struct {
	PublicURL string
	AdminURL  string
	Issuer    string
}

// terminate registers container termination on t.Cleanup. Termination
// failures are logged, not fatal, so they never mask real test failures.
func terminate(t *testing.T, c testcontainers.Container) {
	t.Helper()
	t.Cleanup(func() {
		if err := c.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	})
}

// pingPostgres pings the database until it accepts connections or the
// pingTimeout deadline is reached. Returns an error instead of failing the
// test so the caller decides how to surface the failure.
func pingPostgres(connStr string) error {
	cfg, err := pgx.ParseConfig(connStr)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	for {
		sqlDB := stdlib.OpenDB(*cfg)
		err := sqlDB.PingContext(ctx)
		sqlDB.Close()
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("postgres at %s did not become ready within %s", cfg.Host, pingTimeout)
		case <-time.After(pingRetryWait):
		}
	}
}

// SetupPostgres starts a Postgres container, waits until it accepts
// connections, applies all migrations, and returns the connection string.
// Teardown is registered on t; callers never terminate the container
// themselves. Any failure is fatal to the calling test.
func SetupPostgres(t *testing.T) string {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		postgresImage,
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container (is a container runtime available?): %v", err)
	}
	terminate(t, pgContainer)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get postgres connection string: %v", err)
	}

	if err := pingPostgres(connStr); err != nil {
		t.Fatalf("%v", err)
	}

	RunMigrations(t, connStr)

	return connStr
}

// SetupHydra starts an Ory Hydra container in dev mode with an in-memory
// DSN and JWT access tokens. Teardown is registered on t; callers never
// terminate the container themselves. Any failure is fatal to the calling
// test.
func SetupHydra(t *testing.T) *HydraEnv {
	t.Helper()

	env, container, err := startHydra(t)
	if err != nil {
		t.Fatalf("%v", err)
	}
	terminate(t, container)

	return env
}

// startHydra starts the Hydra container without registering cleanup, leaving
// teardown to the caller.
func startHydra(t *testing.T) (*HydraEnv, testcontainers.Container, error) {
	t.Helper()

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        hydraImage,
		User:         "1000:1000",
		ExposedPorts: []string{"4444/tcp", "4445/tcp"},
		Env: map[string]string{
			"DSN":                     "memory",
			"URLS_SELF_ISSUER":        hydraIssuer,
			"URLS_LOGIN":              "http://127.0.0.1:8000/login",
			"URLS_CONSENT":            "http://127.0.0.1:8000/consent",
			"SECRETS_SYSTEM":          "test-secret-that-needs-to-be-long-enough",
			"STRATEGIES_ACCESS_TOKEN": "jwt",
			"CORS_DEBUG":              "1",
			"LOG_LEVEL":               "info",
		},
		Cmd:        []string{"serve", "all", "--dev"},
		WaitingFor: wait.ForHTTP("/health/ready").WithPort("4445/tcp"),
	}

	hydraContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start hydra container (is a container runtime available?): %v", err)
	}

	host, err := hydraContainer.Host(ctx)
	if err != nil {
		hydraContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to get hydra container host: %v", err)
	}

	publicPort, err := hydraContainer.MappedPort(ctx, "4444")
	if err != nil {
		hydraContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to get hydra public port: %v", err)
	}

	adminPort, err := hydraContainer.MappedPort(ctx, "4445")
	if err != nil {
		hydraContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to get hydra admin port: %v", err)
	}

	return &HydraEnv{
		PublicURL: fmt.Sprintf("http://%s:%s", host, publicPort.Port()),
		AdminURL:  fmt.Sprintf("http://%s:%s", host, adminPort.Port()),
		Issuer:    hydraIssuer,
	}, hydraContainer, nil
}

// SetupOpenFGA starts an OpenFGA container with preshared-key auth, creates
// a store, writes the service authorization model, and returns a client
// configured with the resulting store and model IDs. Teardown is registered
// on t; callers never terminate the container themselves. Any failure is
// fatal to the calling test.
func SetupOpenFGA(t *testing.T) *openfga.Client {
	t.Helper()

	client, container, err := startOpenFGA(t)
	if err != nil {
		t.Fatalf("%v", err)
	}
	terminate(t, container)

	return client
}

// startOpenFGA starts the OpenFGA container and wires store + model without
// registering cleanup, leaving teardown to the caller.
func startOpenFGA(t *testing.T) (*openfga.Client, testcontainers.Container, error) {
	t.Helper()

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        openfgaImage,
		ExposedPorts: []string{"8080/tcp"},
		Env: map[string]string{
			"OPENFGA_AUTHN_METHOD":         "preshared",
			"OPENFGA_AUTHN_PRESHARED_KEYS": openFGAPresharedKey,
		},
		Cmd:        []string{"run"},
		WaitingFor: wait.ForHTTP("/healthz").WithPort("8080/tcp"),
	}

	fgaContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start openfga container (is a container runtime available?): %v", err)
	}

	host, err := fgaContainer.Host(ctx)
	if err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to get openfga container host: %v", err)
	}

	port, err := fgaContainer.MappedPort(ctx, "8080")
	if err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to get openfga container port: %v", err)
	}

	fgaURL := fmt.Sprintf("http://%s:%s", host, port.Port())

	u, err := url.Parse(fgaURL)
	if err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to parse openfga URL: %v", err)
	}

	logger := logging.NewNoopLogger()
	monitor := monitoring.NewNoopMonitor("hook-service-test", logger)
	tracer := tracing.NewNoopTracer()

	fgaClient := openfga.NewClient(&openfga.Config{
		ApiScheme: u.Scheme,
		ApiHost:   u.Host,
		ApiToken:  openFGAPresharedKey,
		Tracer:    tracer,
		Monitor:   monitor,
		Logger:    logger,
	})

	storeID, err := fgaClient.CreateStore(ctx, "hook-service-test")
	if err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to create openfga store: %v", err)
	}
	if err := fgaClient.SetStoreID(ctx, storeID); err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to set openfga store ID: %v", err)
	}

	authzModel := internalAuthz.NewAuthorizationModelProvider("v0").GetModel()

	modelID, err := fgaClient.WriteModel(
		ctx,
		&client.ClientWriteAuthorizationModelRequest{
			TypeDefinitions: authzModel.TypeDefinitions,
			SchemaVersion:   authzModel.SchemaVersion,
			Conditions:      authzModel.Conditions,
		},
	)
	if err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to write openfga authorization model: %v", err)
	}
	if err := fgaClient.SetAuthorizationModelID(ctx, modelID); err != nil {
		fgaContainer.Terminate(ctx) //nolint:errcheck
		return nil, nil, fmt.Errorf("failed to set openfga authorization model ID: %v", err)
	}

	return fgaClient, fgaContainer, nil
}
