// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	hydra "github.com/ory/hydra-client-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_tracer.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_verifier.go -source=./interfaces.go

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ToLower(name)
	return name
}

// configurePodmanSocket sets DOCKER_HOST to the podman socket path derived from
// XDG_RUNTIME_DIR, unless DOCKER_HOST is already set in the environment.
// This allows testcontainers to use podman as the container runtime.
func configurePodmanSocket() {
	if os.Getenv("DOCKER_HOST") != "" {
		return
	}
	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		return
	}
	socketPath := xdgRuntime + "/podman/podman.sock"
	if _, err := os.Stat(socketPath); err == nil {
		os.Setenv("DOCKER_HOST", "unix://"+socketPath) //nolint:errcheck
	}
}

func setupTestHydra(t *testing.T) (string, string, testcontainers.Container) {
	t.Helper()
	configurePodmanSocket()
	ctx := context.Background()

	containerName := fmt.Sprintf("hook-hydra-%s", sanitizeName(t.Name()))

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
				"URLS_SELF_ISSUER":        "http://127.0.0.1:4444/", // Set explicitly
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
			t.Fatalf("Failed to start Hydra container: %v", err)
		}

		// Print logs for debugging
		logReader, _ := hydraContainer.Logs(ctx)
		if logReader != nil {
			bytes, _ := io.ReadAll(logReader)
			t.Logf("Hydra container logs:\n%s", string(bytes))
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

func TestJWTAuthentication(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	ctx := context.Background()

	// Spin up Hydra using testcontainers
	publicURL, adminURL, hydraContainer := setupTestHydra(t)
	if hydraContainer == nil {
		return // skipped due to Docker unavailability
	}
	defer func() {
		if err := hydraContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	// In testcontainers, the publicURL gets a random port, but hydra was configured with URLS_SELF_ISSUER=http://127.0.0.1:4444/
	// For OIDC discovery, we might need to actually use the public URL to fetch the manifest.
	// Since SkipIssuerCheck isn't easily toggleable in NewJWTAuthenticator if we don't supply JWKS directly:
	// We'll just fetch JWKS directly with JWKS URL to avoid the strict OIDC issuer match on discovery!
	jwksURL := fmt.Sprintf("%s/.well-known/jwks.json", publicURL)

	// Create a valid client
	clientID, clientSecret, err := setupHydraClient(ctx, adminURL, "Test Client")
	if err != nil {
		t.Fatalf("failed to setup hydra client: %v", err)
	}

	validToken, err := getJWTToken(ctx, publicURL, clientID, clientSecret)
	if err != nil {
		t.Fatalf("failed to get valid JWT token: %v", err)
	}

	// Create an invalid client for wrong subject
	wrongClientID, wrongClientSecret, err := setupHydraClient(ctx, adminURL, "Wrong Subject Client")
	if err != nil {
		t.Fatalf("failed to create wrong subject client: %v", err)
	}
	wrongToken, err := getJWTToken(ctx, publicURL, wrongClientID, wrongClientSecret)
	if err != nil {
		t.Fatalf("failed to get JWT token for wrong client: %v", err)
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)

	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("INFO: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("DEBUG: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).Do(func(msg string) { t.Logf("INFO: %s", msg) }).AnyTimes()
	mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(ctx, trace.SpanFromContext(ctx)).AnyTimes()

	mockSecurity := NewMockSecurityLoggerInterface(ctrl)
	mockSecurity.EXPECT().AuthzFailure(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Security().Return(mockSecurity).AnyTimes()

	// Create Authenticator
	// Use the configured URLS_SELF_ISSUER (http://127.0.0.1:4444/) instead of the dynamic publicURL to match the 'iss' claim
	verifier, err := NewJWTAuthenticator(ctx, "http://127.0.0.1:4444/", jwksURL, []string{clientID}, "", mockTracer, mockMonitor, mockLogger)
	if err != nil {
		t.Fatalf("failed to create JWT authenticator: %v", err)
	}

	middleware := NewMiddleware(verifier, mockTracer, mockMonitor, mockLogger)

	// Create a dummy server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "panic in handler: %v", rec)
			}
		}()
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	server := httptest.NewServer(middleware.Authenticate()(handler))
	defer server.Close()

	client := server.Client()
	client.Timeout = 10 * time.Second

	t.Run("Valid JWT Token Allowed", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
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
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)

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
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
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

		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
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
