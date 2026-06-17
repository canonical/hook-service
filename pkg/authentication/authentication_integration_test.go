// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	hydra "github.com/ory/hydra-client-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/migrations"
	"github.com/canonical/hook-service/pkg/authentication"
	"github.com/canonical/hook-service/pkg/web"
)

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ToLower(name)
	return name
}

func setupTestPostgres(t *testing.T, suffix string) (string, *postgres.PostgresContainer) {
	t.Helper()
	ctx := context.Background()

	containerName := fmt.Sprintf("hook-auth-postgres-%s-%d", sanitizeName(t.Name()+suffix), time.Now().UnixNano()%10000)

	var pgContainer *postgres.PostgresContainer
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("Skipping: Docker not available (%v)", r)
			}
		}()
		var err error
		pgContainer, err = postgres.Run(ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("testdb"),
			postgres.WithUsername("testuser"),
			postgres.WithPassword("testpass"),
			testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
				ContainerRequest: testcontainers.ContainerRequest{
					Name: containerName,
				},
			}),
		)
		if err != nil {
			t.Skipf("Skipping: Failed to start PostgreSQL container: %v", err)
		}
	}()

	if pgContainer == nil {
		return "", nil
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		config, err := pgx.ParseConfig(connStr)
		if err != nil {
			t.Fatalf("Failed to parse config: %v", err)
		}
		sqlDB := stdlib.OpenDB(*config)
		if err := sqlDB.Ping(); err == nil {
			sqlDB.Close()
			break
		}
		sqlDB.Close()
		if i < maxRetries-1 {
			time.Sleep(time.Second)
		}
	}

	return connStr, pgContainer
}

func runMigrationsOnDSN(t *testing.T, connStr string) {
	t.Helper()
	config, err := pgx.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("Failed to parse DSN: %v", err)
	}

	sqlDB := stdlib.OpenDB(*config)
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.EmbedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("Failed to set dialect: %v", err)
	}

	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
}

func setupTestHydra(t *testing.T) (string, string, testcontainers.Container) {
	t.Helper()
	ctx := context.Background()

	containerName := fmt.Sprintf("hook-hydra-auth-int-%s-%d", sanitizeName(t.Name()), time.Now().UnixNano()%10000)

	var hydraContainer testcontainers.Container

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("Skipping: Docker not available (%v)", r)
			}
		}()

		req := testcontainers.ContainerRequest{
			Image:        "oryd/hydra:v25.4.0",
			Name:         containerName,
			User:         "1000:1000",
			ExposedPorts: []string{"4444/tcp", "4445/tcp"},
			Env: map[string]string{
				"DSN":                     "memory",
				"URLS_SELF_ISSUER":        "http://127.0.0.1:4444/",
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

		var err error
		hydraContainer, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			if hydraContainer != nil {
				logReader, _ := hydraContainer.Logs(ctx)
				if logReader != nil {
					bytes, _ := io.ReadAll(logReader)
					t.Logf("Hydra container logs:\n%s", string(bytes))
				}
			}
			t.Skipf("Skipping: Failed to start Hydra container: %v", err)
		}
	}()

	if hydraContainer == nil {
		return "", "", nil
	}

	publicPort, err := hydraContainer.MappedPort(ctx, "4444")
	if err != nil {
		t.Fatalf("Failed to get mapped public port: %v", err)
	}
	adminPort, err := hydraContainer.MappedPort(ctx, "4445")
	if err != nil {
		t.Fatalf("Failed to get mapped admin port: %v", err)
	}

	hostIP, err := hydraContainer.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	publicURL := fmt.Sprintf("http://%s:%s", hostIP, publicPort.Port())
	adminURL := fmt.Sprintf("http://%s:%s", hostIP, adminPort.Port())

	return publicURL, adminURL, hydraContainer
}

func setupHydraClient(ctx context.Context, adminURL, clientName string) (string, string, error) {
	configuration := hydra.NewConfiguration()
	configuration.Servers = []hydra.ServerConfiguration{
		{
			URL: adminURL,
		},
	}
	apiClient := hydra.NewAPIClient(configuration)

	client := hydra.NewOAuth2Client()
	client.SetClientName(clientName)
	client.SetGrantTypes([]string{"client_credentials"})

	createdClient, _, err := apiClient.OAuth2API.CreateOAuth2Client(ctx).OAuth2Client(*client).Execute()
	if err != nil {
		return "", "", fmt.Errorf("failed to create hydra client via SDK: %w", err)
	}

	if createdClient.ClientId == nil || createdClient.ClientSecret == nil {
		return "", "", fmt.Errorf("hydra client creation succeeded but missing credentials")
	}

	return *createdClient.ClientId, *createdClient.ClientSecret, nil
}

func getJWTToken(ctx context.Context, publicURL, clientID, clientSecret string) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	tokenURL := fmt.Sprintf("%s/oauth2/token", publicURL)
	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get JWT token: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse token response: %v", err)
	}

	return result.AccessToken, nil
}

func TestIntegration_JWTAuthentication(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Spin up Postgres container
	connStr, pgContainer := setupTestPostgres(t, "auth")
	if pgContainer == nil {
		return // skipped due to Docker unavailability
	}
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate Postgres container: %v", err)
		}
	}()

	// 2. Run migrations on Postgres container
	runMigrationsOnDSN(t, connStr)

	// 3. Spin up Ory Hydra container
	publicURL, adminURL, hydraContainer := setupTestHydra(t)
	if hydraContainer == nil {
		return // skipped due to Docker unavailability
	}
	defer func() {
		if err := hydraContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate Hydra container: %v", err)
		}
	}()

	// Configure Hydra clients
	clientID, clientSecret, err := setupHydraClient(ctx, adminURL, "Test Client")
	if err != nil {
		t.Fatalf("failed to setup hydra client: %v", err)
	}

	validToken, err := getJWTToken(ctx, publicURL, clientID, clientSecret)
	if err != nil {
		t.Fatalf("failed to get valid JWT token: %v", err)
	}

	wrongClientID, wrongClientSecret, err := setupHydraClient(ctx, adminURL, "Wrong Subject Client")
	if err != nil {
		t.Fatalf("failed to create wrong subject client: %v", err)
	}

	wrongToken, err := getJWTToken(ctx, publicURL, wrongClientID, wrongClientSecret)
	if err != nil {
		t.Fatalf("failed to get JWT token for wrong client: %v", err)
	}

	// 4. Initialize dependencies for web.NewRouter
	tracer := tracing.NewNoopTracer()
	logger := logging.NewNoopLogger()
	monitor := monitoring.NewNoopMonitor("test", logger)

	// Create Authenticator
	jwksURL := fmt.Sprintf("%s/.well-known/jwks.json", publicURL)
	verifier, err := authentication.NewJWTAuthenticator(ctx, "http://127.0.0.1:4444/", jwksURL, []string{clientID}, "", tracer, monitor, logger)
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
		"",                      // API token
		true,                    // authenticationEnabled
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
