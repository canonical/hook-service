// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package groups_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

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

type IntegrationClient struct {
	t       *testing.T
	baseURL string
	client  *http.Client
}

func (c *IntegrationClient) Request(method, path string, body interface{}) (int, []byte) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("failed to marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		c.t.Fatalf("failed to create request: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("failed to read response body: %v", err)
	}

	return resp.StatusCode, respBody
}

func (c *IntegrationClient) CreateGroup() string {
	name := fmt.Sprintf("test-group-%d", time.Now().UnixNano())
	body := map[string]interface{}{
		"name":        name,
		"description": "A test group",
		"type":        "local",
	}
	status, respBody := c.Request(http.MethodPost, "/groups", body)
	if status != http.StatusOK {
		c.t.Fatalf("expected status OK, got %d. Body: %s", status, string(respBody))
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := json.Unmarshal(respBody, &resp)
	if err != nil {
		c.t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data) == 0 {
		c.t.Fatal("expected created group data, got empty list")
	}
	return resp.Data[0].ID
}

func (c *IntegrationClient) DeleteGroup(groupID string) {
	status, _ := c.Request(http.MethodDelete, "/groups/"+groupID, nil)
	if status != http.StatusOK {
		c.t.Fatalf("failed to delete group %s, status: %d", groupID, status)
	}
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return strings.ToLower(name)
}

func setupTestPostgres(t *testing.T) (string, *postgres.PostgresContainer) {
	t.Helper()

	ctx := context.Background()
	containerName := fmt.Sprintf("hook-authz-%s", sanitizeName(t.Name()))

	var pgContainer *postgres.PostgresContainer
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("Skipping: container runtime not available (%v)", r)
			}
		}()
		var err error
		pgContainer, err = postgres.Run(ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("testdb"),
			postgres.WithUsername("testuser"),
			postgres.WithPassword("testpass"),
			testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
				ContainerRequest: testcontainers.ContainerRequest{Name: containerName},
			}),
		)
		if err != nil {
			t.Skipf("Skipping: container runtime not available (%v)", err)
		}
	}()

	if pgContainer == nil {
		return "", nil
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	for i := 0; i < 10; i++ {
		cfg, err := pgx.ParseConfig(connStr)
		if err != nil {
			t.Fatalf("Failed to parse config: %v", err)
		}
		sqlDB := stdlib.OpenDB(*cfg)
		if err := sqlDB.Ping(); err == nil {
			sqlDB.Close()
			break
		}
		sqlDB.Close()
		if i < 9 {
			time.Sleep(time.Second)
		}
	}

	return connStr, pgContainer
}

func runMigrations(t *testing.T, connStr string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("Failed to parse DSN: %v", err)
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.EmbedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("Failed to set dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
}

func setupIntegrationEnv(t *testing.T) (string, func()) {
	t.Helper()

	connStr, pgContainer := setupTestPostgres(t)
	if pgContainer == nil {
		t.Skip("Postgres container is nil, skipping")
	}
	runMigrations(t, connStr)

	logger := logging.NewNoopLogger()
	monitor := monitoring.NewNoopMonitor("hook-service-test", logger)
	tracer := tracing.NewNoopTracer()

	dbClient, err := db.NewDBClient(db.Config{DSN: connStr, MaxConns: 5, MinConns: 1}, tracer, monitor, logger)
	if err != nil {
		pgContainer.Terminate(context.Background()) //nolint:errcheck
		t.Fatalf("Failed to create DB client: %v", err)
	}

	s := storage.NewStorage(dbClient, tracer, monitor, logger)
	authz := authorization.NewAuthorizer(
		openfga.NewNoopClient(tracer, monitor, logger),
		tracer, monitor, logger,
	)

	wpool := pool.NewWorkerPool(1, tracer, monitor, logger)
	tenantValidator := tenants.NewNoopValidator()
	jwtVerifier := authentication.NewNoopVerifier()

	router := web.NewRouter(
		"",    // token
		false, // authenticationEnabled
		wpool,
		s,
		dbClient,
		authz,
		tenantValidator,
		jwtVerifier,
		tracer,
		monitor,
		logger,
	)

	srv := httptest.NewServer(router)

	cleanup := func() {
		srv.Close()
		wpool.Stop()
		dbClient.Close()
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}

	return srv.URL, cleanup
}

func TestGroupLifecycle(t *testing.T) {
	baseURL, cleanup := setupIntegrationEnv(t)
	defer cleanup()

	client := &IntegrationClient{
		t:       t,
		baseURL: baseURL + "/api/v0/authz",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
	groupID := client.CreateGroup()
	defer client.DeleteGroup(groupID)

	t.Run("Get Group", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups/"+groupID, nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if len(resp.Data) == 0 || resp.Data[0].ID != groupID {
			t.Errorf("expected group ID %s, got %v", groupID, resp.Data)
		}
	})

	t.Run("Update Group", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"description": "Updated description",
			"type":        "local",
		}
		status, body := client.Request(http.MethodPut, "/groups/"+groupID, updateBody)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d. Body: %s", status, string(body))
		}

		var resp struct {
			Data []struct {
				ID          string `json:"id"`
				Description string `json:"description"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if len(resp.Data) == 0 || resp.Data[0].Description != "Updated description" {
			t.Errorf("expected updated description, got %v", resp.Data)
		}
	})

	t.Run("List Groups", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, g := range resp.Data {
			if g.ID == groupID {
				found = true
				break
			}
		}
		if !found {
			t.Error("created group not found in list")
		}
	})
}

func TestUserMembership(t *testing.T) {
	baseURL, cleanup := setupIntegrationEnv(t)
	defer cleanup()

	client := &IntegrationClient{
		t:       t,
		baseURL: baseURL + "/api/v0/authz",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
	groupID := client.CreateGroup()
	defer client.DeleteGroup(groupID)

	userID := fmt.Sprintf("test-user-%d@example.com", time.Now().UnixNano())

	t.Run("Add User", func(t *testing.T) {
		body := []string{userID}
		status, _ := client.Request(http.MethodPost, "/groups/"+groupID+"/users", body)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
	})

	t.Run("List Users in Group", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/groups/"+groupID+"/users", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, u := range resp.Data {
			if u.ID == userID {
				found = true
				break
			}
		}
		if !found {
			t.Error("added user not found in group")
		}
	})

	t.Run("List Groups for User", func(t *testing.T) {
		status, body := client.Request(http.MethodGet, "/users/"+userID+"/groups", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		found := false
		for _, g := range resp.Data {
			if g.ID == groupID {
				found = true
				break
			}
		}
		if !found {
			t.Error("group not found in user's groups")
		}
	})

	t.Run("Remove User", func(t *testing.T) {
		status, _ := client.Request(http.MethodDelete, "/groups/"+groupID+"/users/"+userID, nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}

		status, body := client.Request(http.MethodGet, "/groups/"+groupID+"/users", nil)
		if status != http.StatusOK {
			t.Errorf("expected status OK, got %d", status)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		err := json.Unmarshal(body, &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		for _, u := range resp.Data {
			if u.ID == userID {
				t.Error("user still found in group after removal")
			}
		}
	})
}
