// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package hooks_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/testhelpers"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/authentication"
	"github.com/canonical/hook-service/pkg/hooks"
	"github.com/canonical/hook-service/pkg/web"
)

// setupIntegrationEnv spins up a Postgres testcontainer and an httptest.Server
// running the full HTTP router.
func setupIntegrationEnv(t *testing.T) (string, func()) {
	t.Helper()

	connStr := testhelpers.SetupPostgres(t)

	logger := logging.NewNoopLogger()
	monitor := monitoring.NewNoopMonitor("hook-service-test", logger)
	tracer := tracing.NewNoopTracer()
	publisher := kafka.NewNoopPublisher(tracer, monitor, logger)

	dbClient, err := db.NewDBClient(db.Config{DSN: connStr, MaxConns: 5, MinConns: 1}, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to create DB client: %v", err)
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
		publisher,
		config.ModeStandalone,
		tracer,
		monitor,
		logger,
	)

	srv := httptest.NewServer(router)

	cleanup := func() {
		srv.Close()
		wpool.Stop()
		dbClient.Close()
	}

	return srv.URL, cleanup
}

func TestHydraHookGroupRemovalRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	baseURL, cleanup := setupIntegrationEnv(t)
	defer cleanup()

	authzClient := testhelpers.NewIntegrationClient(t, baseURL+"/api/v0/authz")
	hookClient := testhelpers.NewIntegrationClient(t, baseURL+"/api/v0/hook")

	groupName := fmt.Sprintf("test-group-%d", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"name":        groupName,
		"description": "Integration test group for token hook refresh",
		"type":        "local",
	}
	status, createRespBody := authzClient.Request(http.MethodPost, "/groups", createBody)
	if status != http.StatusOK {
		t.Fatalf("expected status OK creating group, got %d: %s", status, string(createRespBody))
	}

	var createResp struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createRespBody, &createResp); err != nil {
		t.Fatalf("failed to unmarshal create group response: %v", err)
	}
	if len(createResp.Data) == 0 {
		t.Fatal("expected created group in response, got empty list")
	}
	groupID := createResp.Data[0].ID
	defer authzClient.DeleteGroup(groupID)

	userEmail := fmt.Sprintf("test-user-%d@example.com", time.Now().UnixNano())
	userSubject := fmt.Sprintf("user-sub-%d", time.Now().UnixNano())

	status, addRespBody := authzClient.Request(http.MethodPost, "/groups/"+groupID+"/users", []string{userEmail})
	if status != http.StatusOK {
		t.Fatalf("expected status OK adding user to group, got %d: %s", status, string(addRespBody))
	}

	t.Run("Initial token issuance populates groups claim", func(t *testing.T) {
		req := hooks.TokenHookRequest{
			Session: &hooks.Session{
				DefaultSession: &hooks.DefaultSession{
					Subject: userSubject,
					Claims: &hooks.IDTokenClaims{
						Extra: map[string]interface{}{
							"email": userEmail,
						},
					},
				},
			},
			Request: hooks.Request{
				ClientID:   "test-client",
				GrantTypes: []string{"authorization_code"},
			},
		}

		hookStatus, hookBody := hookClient.Request(http.MethodPost, "/hydra", req)
		if hookStatus != http.StatusOK {
			t.Fatalf("expected status OK from hydra hook, got %d: %s", hookStatus, string(hookBody))
		}

		var resp hooks.TokenHookResponse
		if err := json.Unmarshal(hookBody, &resp); err != nil {
			t.Fatalf("failed to unmarshal hook response: %v", err)
		}

		atGroups, ok := resp.Session.AccessToken["groups"].([]interface{})
		if !ok || len(atGroups) != 1 || atGroups[0] != groupName {
			t.Fatalf("expected access_token groups [%q], got %#v", groupName, resp.Session.AccessToken["groups"])
		}

		idtGroups, ok := resp.Session.IDToken["groups"].([]interface{})
		if !ok || len(idtGroups) != 1 || idtGroups[0] != groupName {
			t.Fatalf("expected id_token groups [%q], got %#v", groupName, resp.Session.IDToken["groups"])
		}
	})

	t.Run("Refresh after removing user from group clears stale groups claim", func(t *testing.T) {
		delStatus, delBody := authzClient.Request(http.MethodDelete, "/groups/"+groupID+"/users/"+userEmail, nil)
		if delStatus != http.StatusOK {
			t.Fatalf("expected status OK removing user from group, got %d: %s", delStatus, string(delBody))
		}

		refreshReq := hooks.TokenHookRequest{
			Session: &hooks.Session{
				Extra: map[string]interface{}{
					"groups":       []string{groupName},
					"custom_claim": "keep-me",
				},
				DefaultSession: &hooks.DefaultSession{
					Subject: userSubject,
					Claims: &hooks.IDTokenClaims{
						Extra: map[string]interface{}{
							"groups": []string{groupName},
							"email":  userEmail,
						},
					},
				},
			},
			Request: hooks.Request{
				ClientID:   "test-client",
				GrantTypes: []string{"refresh_token"},
			},
		}

		hookStatus, hookBody := hookClient.Request(http.MethodPost, "/hydra", refreshReq)
		if hookStatus != http.StatusOK {
			t.Fatalf("expected status OK from hydra hook on refresh, got %d: %s", hookStatus, string(hookBody))
		}

		var resp hooks.TokenHookResponse
		if err := json.Unmarshal(hookBody, &resp); err != nil {
			t.Fatalf("failed to unmarshal hook response: %v", err)
		}

		atGroups, ok := resp.Session.AccessToken["groups"].([]interface{})
		if !ok {
			t.Fatalf("expected access_token groups to be a JSON array, got %T (%#v)", resp.Session.AccessToken["groups"], resp.Session.AccessToken["groups"])
		}
		if len(atGroups) != 0 {
			t.Fatalf("expected access_token groups to be empty after removal, got %#v", atGroups)
		}

		idtGroups, ok := resp.Session.IDToken["groups"].([]interface{})
		if !ok {
			t.Fatalf("expected id_token groups to be a JSON array, got %T (%#v)", resp.Session.IDToken["groups"], resp.Session.IDToken["groups"])
		}
		if len(idtGroups) != 0 {
			t.Fatalf("expected id_token groups to be empty after removal, got %#v", idtGroups)
		}

		if got := resp.Session.AccessToken["custom_claim"]; got != "keep-me" {
			t.Fatalf("expected access_token custom_claim %q to be preserved, got %#v", "keep-me", got)
		}

		if got := resp.Session.IDToken["email"]; got != userEmail {
			t.Fatalf("expected id_token email %q to be preserved, got %#v", userEmail, got)
		}
	})
}
