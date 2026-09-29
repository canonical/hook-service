// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/authentication"
)

func TestNewRouterReportsDeploymentMode(t *testing.T) {
	tests := []struct {
		name                 string
		federatedServiceName string
		deploymentMode       config.DeploymentMode
	}{
		{
			name:                 "standalone mode under the default federated service name",
			federatedServiceName: config.DefaultFederatedServiceName,
			deploymentMode:       config.ModeStandalone,
		},
		{
			name:                 "platform mode under a non-default federated service name",
			federatedServiceName: "portal",
			deploymentMode:       config.ModePlatform,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := logging.NewNoopLogger()
			tracer := tracing.NewNoopTracer()
			monitor := monitoring.NewNoopMonitor(tt.federatedServiceName, logger)

			router := NewRouter(
				"",    // token
				false, // authenticationEnabled
				nil,   // wpool
				nil,   // storage
				nil,   // dbClient
				nil,   // authz
				tenants.NewNoopValidator(),
				authentication.NewNoopVerifier(),
				nil, // publisher
				tt.deploymentMode,
				tracer,
				monitor,
				logger,
			)

			req := httptest.NewRequest(http.MethodGet, "/api/v0/status", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			res := w.Result()
			defer func() { _ = res.Body.Close() }()

			if res.StatusCode != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, res.StatusCode)
			}

			var body struct {
				Status string                `json:"status"`
				Mode   config.DeploymentMode `json:"mode"`
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode status response: %v", err)
			}
			if body.Status != "ok" {
				t.Errorf("expected status %q, got %q", "ok", body.Status)
			}
			if body.Mode != tt.deploymentMode {
				t.Errorf("expected mode %q, got %q", tt.deploymentMode, body.Mode)
			}
		})
	}
}
