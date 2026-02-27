// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package authorization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	reflect "reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/http/types"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/migrations"
	groups_api "github.com/canonical/hook-service/pkg/groups"
	v0_authz "github.com/canonical/identity-platform-api/v0/authorization"
	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/protobuf/encoding/protojson"
)

//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_authorization.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//goʻgenerate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

// testClient wraps an httptest.Server with helper methods for the authorization API.
type testClient struct {
	t      *testing.T
	server *httptest.Server
	http   *http.Client
}

func (c *testClient) Request(method, path string, body interface{}) (int, []byte) {
	c.t.Helper()

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("failed to marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, c.server.URL+path, reqBody)
	if err != nil {
		c.t.Fatalf("failed to create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("request to %s %s failed: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return strings.ToLower(name)
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

func setupTestPostgres(t *testing.T) (string, *postgres.PostgresContainer) {
	t.Helper()

	// Use podman socket if Docker is not already configured.
	configurePodmanSocket()

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

// newIntegrationServer spins up Postgres, runs migrations, and wires all gRPC-gateway
// handlers directly on a runtime.ServeMux (avoids import cycle with pkg/web).
func newIntegrationServer(t *testing.T) (*testClient, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	connStr, pgContainer := setupTestPostgres(t)
	if pgContainer == nil {
		return nil, func() {}
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

	// Wire gRPC-gateway directly to avoid the pkg/web → pkg/authorization import cycle.
	gwMux := runtime.NewServeMux(
		runtime.WithForwardResponseRewriter(types.ForwardErrorResponseRewriter),
		runtime.WithDisablePathLengthFallback(),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{UseProtoNames: true},
		}),
	)

	authzSvc := NewService(s, authz, tracer, monitor, logger)
	groupSvc := groups_api.NewService(s, authz, tracer, monitor, logger)

	ctx := context.Background()
	v0_authz.RegisterAppAuthorizationServiceHandlerServer(ctx, gwMux,
		NewGrpcServer(authzSvc, tracer, monitor, logger),
	)
	v0_groups.RegisterAuthzGroupsServiceHandlerServer(ctx, gwMux,
		groups_api.NewGrpcServer(groupSvc, tracer, monitor, logger),
	)

	srv := httptest.NewServer(gwMux)

	cleanup := func() {
		srv.Close()
		dbClient.Close()
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}

	return &testClient{t: t, server: srv, http: srv.Client()}, cleanup
}

// createTestGroup creates a group via the groups API and returns its ID.
func createTestGroup(t *testing.T, client *testClient, name string) string {
	t.Helper()

	body := map[string]interface{}{
		"name":        name,
		"description": "integration test group",
		"type":        "local",
	}
	statusCode, respBody := client.Request(http.MethodPost, "/api/v0/authz/groups", body)
	if statusCode != http.StatusOK {
		t.Fatalf("failed to create test group %q (status %d): %s", name, statusCode, string(respBody))
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("failed to unmarshal create group response: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("create group returned empty data for group %q", name)
	}
	return resp.Data[0].ID
}

func TestGrpcServer_GetAllowedAppsInGroup(t *testing.T) {
	tests := []struct {
		name         string
		groupID      string
		expectResult []string
		expectErr    error
		wantErr      error
		wantResp     *v0_authz.GetAllowedAppsInGroupResp
	}{
		{
			name:         "Success with apps",
			groupID:      "group1",
			expectResult: []string{"app1", "app2"},
			expectErr:    nil,
			wantErr:      nil,
			wantResp: &v0_authz.GetAllowedAppsInGroupResp{
				Data:    []*v0_authz.App{{ClientId: "app1"}, {ClientId: "app2"}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Allowed apps for group"; return &s }(),
			},
		},
		{
			name:         "Success with no apps",
			groupID:      "group2",
			expectResult: []string{},
			expectErr:    nil,
			wantErr:      nil,
			wantResp: &v0_authz.GetAllowedAppsInGroupResp{
				Data:    []*v0_authz.App{},
				Status:  http.StatusOK,
				Message: func() *string { s := "Allowed apps for group"; return &s }(),
			},
		},
		{
			name:         "Service returns error",
			groupID:      "group3",
			expectResult: nil,
			expectErr:    errors.New("service error"),
			wantErr:      errors.New("service error"),
			wantResp:     nil,
		},
		{
			name:         "Group not found",
			groupID:      "group-not-found",
			expectResult: nil,
			expectErr:    ErrGroupNotFound,
			wantErr:      ErrGroupNotFound,
			wantResp:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockServiceInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockSvc.EXPECT().GetAllowedAppsInGroup(gomock.Any(), tt.groupID).Return(tt.expectResult, tt.expectErr)

			req := &v0_authz.GetAllowedAppsInGroupReq{GroupId: tt.groupID}
			resp, err := server.GetAllowedAppsInGroup(context.Background(), req)

			// expected error handling
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}

				// If the expected error is a sentinel, check for gRPC NotFound code
				if errors.Is(tt.wantErr, ErrGroupNotFound) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for group not found, got %v", err)
					}
					return
				}

				// For generic errors, ensure we return an internal gRPC error
				st, ok := status.FromError(err)
				if !ok || st.Code() != codes.Internal {
					t.Errorf("expected gRPC Internal for error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("GetAllowedAppsInGroup() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcServer_AddAllowedAppToGroup(t *testing.T) {
	tests := []struct {
		name      string
		groupID   string
		clientID  string
		expectErr error
		wantErr   error
	}{
		{
			name:      "Success",
			groupID:   "group1",
			clientID:  "app1",
			expectErr: nil,
			wantErr:   nil,
		},
		{
			name:      "Service returns error",
			groupID:   "group2",
			clientID:  "app2",
			expectErr: errors.New("service error"),
			wantErr:   errors.New("service error"),
		},
		{
			name:      "Group not found",
			groupID:   "group-not-found",
			clientID:  "app3",
			expectErr: ErrGroupNotFound,
			wantErr:   ErrGroupNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockServiceInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockSvc.EXPECT().AddAllowedAppToGroup(gomock.Any(), tt.groupID, tt.clientID).Return(tt.expectErr)

			req := &v0_authz.AddAllowedAppToGroupReq{GroupId: tt.groupID, App: &v0_authz.App{ClientId: tt.clientID}}
			resp, err := server.AddAllowedAppToGroup(context.Background(), req)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}

				if errors.Is(tt.wantErr, ErrGroupNotFound) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for group not found, got %v", err)
					}
					return
				}

				st, ok := status.FromError(err)
				if !ok || st.Code() != codes.Internal {
					t.Errorf("expected gRPC Internal for error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcServer_RemoveAllowedAppFromGroup(t *testing.T) {
	tests := []struct {
		name      string
		groupID   string
		appID     string
		expectErr error
		wantErr   error
	}{
		{
			name:      "Success",
			groupID:   "group1",
			appID:     "app1",
			expectErr: nil,
			wantErr:   nil,
		},
		{
			name:      "Service returns error",
			groupID:   "group2",
			appID:     "app2",
			expectErr: errors.New("service error"),
			wantErr:   errors.New("service error"),
		},
		{
			name:      "Group not found",
			groupID:   "group-not-found",
			appID:     "app3",
			expectErr: ErrGroupNotFound,
			wantErr:   ErrGroupNotFound,
		},
		{
			name:      "App does not exist in group",
			groupID:   "group1",
			appID:     "app-not-in-group",
			expectErr: ErrAppDoesNotExistInGroup,
			wantErr:   ErrAppDoesNotExistInGroup,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockServiceInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockSvc.EXPECT().RemoveAllowedAppFromGroup(gomock.Any(), tt.groupID, tt.appID).Return(tt.expectErr)

			req := &v0_authz.RemoveAllowedAppFromGroupReq{GroupId: tt.groupID, AppId: tt.appID}
			resp, err := server.RemoveAllowedAppFromGroup(context.Background(), req)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}

				if errors.Is(tt.wantErr, ErrGroupNotFound) || errors.Is(tt.wantErr, ErrAppDoesNotExistInGroup) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for not found errors, got %v", err)
					}
					return
				}

				st, ok := status.FromError(err)
				if !ok || st.Code() != codes.Internal {
					t.Errorf("expected gRPC Internal for error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcServer_RemoveAllowedAppsFromGroup(t *testing.T) {
	tests := []struct {
		name      string
		groupID   string
		expectErr error
		wantErr   error
	}{
		{
			name:      "Success",
			groupID:   "group1",
			expectErr: nil,
			wantErr:   nil,
		},
		{
			name:      "Service returns error",
			groupID:   "group2",
			expectErr: errors.New("service error"),
			wantErr:   errors.New("service error"),
		},
		{
			name:      "Group not found",
			groupID:   "group-not-found",
			expectErr: ErrGroupNotFound,
			wantErr:   ErrGroupNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockServiceInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockSvc.EXPECT().RemoveAllAllowedAppsFromGroup(gomock.Any(), tt.groupID).Return(tt.expectErr)

			req := &v0_authz.RemoveAllowedAppsFromGroupReq{GroupId: tt.groupID}
			resp, err := server.RemoveAllowedAppsFromGroup(context.Background(), req)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}

				if errors.Is(tt.wantErr, ErrGroupNotFound) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for group not found, got %v", err)
					}
					return
				}

				st, ok := status.FromError(err)
				if !ok || st.Code() != codes.Internal {
					t.Errorf("expected gRPC Internal for error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcServer_GetAllowedGroupsForApp(t *testing.T) {
	tests := []struct {
		name         string
		appID        string
		expectResult []string
		expectErr    error
		wantErr      error
		wantResp     *v0_authz.GetAllowedGroupsForAppResp
	}{
		{
			name:         "Success with groups",
			appID:        "app1",
			expectResult: []string{"group1", "group2"},
			expectErr:    nil,
			wantErr:      nil,
			wantResp: &v0_authz.GetAllowedGroupsForAppResp{
				Data:    []*v0_authz.Group{{GroupId: "group1"}, {GroupId: "group2"}},
				Status:  http.StatusOK,
				Message: func() *string { s := "List of groups allowed for app"; return &s }(),
			},
		},
		{
			name:         "Success with no groups",
			appID:        "app2",
			expectResult: []string{},
			expectErr:    nil,
			wantErr:      nil,
			wantResp: &v0_authz.GetAllowedGroupsForAppResp{
				Data:    []*v0_authz.Group{},
				Status:  http.StatusOK,
				Message: func() *string { s := "List of groups allowed for app"; return &s }(),
			},
		},
		{
			name:         "Service returns error",
			appID:        "app3",
			expectResult: nil,
			expectErr:    errors.New("service error"),
			wantErr:      errors.New("service error"),
			wantResp:     nil,
		},
		{
			name:         "App not found",
			appID:        "app-not-found",
			expectResult: nil,
			expectErr:    ErrAppDoesNotExist,
			wantErr:      ErrAppDoesNotExist,
			wantResp:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockServiceInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockSvc.EXPECT().GetAllowedGroupsForApp(gomock.Any(), tt.appID).Return(tt.expectResult, tt.expectErr)

			req := &v0_authz.GetAllowedGroupsForAppReq{AppId: tt.appID}
			resp, err := server.GetAllowedGroupsForApp(context.Background(), req)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}

				if errors.Is(tt.wantErr, ErrAppDoesNotExist) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for app not found, got %v", err)
					}
					return
				}

				st, ok := status.FromError(err)
				if !ok || st.Code() != codes.Internal {
					t.Errorf("expected gRPC Internal for error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("GetAllowedGroupsForApp() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcServer_RemoveAllowedGroupsForApp(t *testing.T) {
	tests := []struct {
		name      string
		appID     string
		expectErr error
		wantErr   error
	}{
		{
			name:      "Success",
			appID:     "app1",
			expectErr: nil,
			wantErr:   nil,
		},
		{
			name:      "Service returns error",
			appID:     "app2",
			expectErr: errors.New("service error"),
			wantErr:   errors.New("service error"),
		},
		{
			name:      "App not found",
			appID:     "app-not-found",
			expectErr: ErrAppDoesNotExist,
			wantErr:   ErrAppDoesNotExist,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockSvc := NewMockServiceInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockSvc.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), tt.appID).Return(tt.expectErr)

			req := &v0_authz.RemoveAllowedGroupsForAppReq{AppId: tt.appID}
			resp, err := server.RemoveAllowedGroupsForApp(context.Background(), req)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}

				if errors.Is(tt.wantErr, ErrAppDoesNotExist) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for app not found, got %v", err)
					}
					return
				}

				st, ok := status.FromError(err)
				if !ok || st.Code() != codes.Internal {
					t.Errorf("expected gRPC Internal for error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcServer_ValidationErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := NewMockServiceInterface(ctrl)
	mockTracer := NewMockTracingInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)

	server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

	// tracer.Start is always called before validation returns
	mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background())).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "GetAllowedAppsInGroup empty group id",
			call: func() error {
				_, err := server.GetAllowedAppsInGroup(context.Background(), &v0_authz.GetAllowedAppsInGroupReq{GroupId: ""})
				return err
			},
		},
		{
			name: "AddAllowedAppToGroup empty app",
			call: func() error {
				_, err := server.AddAllowedAppToGroup(context.Background(), &v0_authz.AddAllowedAppToGroupReq{GroupId: "group1", App: &v0_authz.App{ClientId: ""}})
				return err
			},
		},
		{
			name: "AddAllowedAppToGroup empty group id",
			call: func() error {
				_, err := server.AddAllowedAppToGroup(context.Background(), &v0_authz.AddAllowedAppToGroupReq{GroupId: "", App: &v0_authz.App{ClientId: "app1"}})
				return err
			},
		},
		{
			name: "RemoveAllowedAppFromGroup empty group id",
			call: func() error {
				_, err := server.RemoveAllowedAppFromGroup(context.Background(), &v0_authz.RemoveAllowedAppFromGroupReq{GroupId: "", AppId: "app1"})
				return err
			},
		},
		{
			name: "RemoveAllowedAppFromGroup empty app id",
			call: func() error {
				_, err := server.RemoveAllowedAppFromGroup(context.Background(), &v0_authz.RemoveAllowedAppFromGroupReq{GroupId: "group1", AppId: ""})
				return err
			},
		},
		{
			name: "RemoveAllowedAppsFromGroup empty group id",
			call: func() error {
				_, err := server.RemoveAllowedAppsFromGroup(context.Background(), &v0_authz.RemoveAllowedAppsFromGroupReq{GroupId: ""})
				return err
			},
		},
		{
			name: "GetAllowedGroupsForApp empty app id",
			call: func() error {
				_, err := server.GetAllowedGroupsForApp(context.Background(), &v0_authz.GetAllowedGroupsForAppReq{AppId: ""})
				return err
			},
		},
		{
			name: "RemoveAllowedGroupsForApp empty app id",
			call: func() error {
				_, err := server.RemoveAllowedGroupsForApp(context.Background(), &v0_authz.RemoveAllowedGroupsForAppReq{AppId: ""})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error for %s, got %v", tt.name, err)
			}
			if st.Code() != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument for %s, got %v", tt.name, st.Code())
			}
		})
	}
}

// Authorization API routes (full paths as matched by gRPC-gateway):
//
//   GET    /api/v0/authz/groups/{group_id}/apps          GetAllowedAppsInGroup
//   POST   /api/v0/authz/groups/{group_id}/apps          AddAllowedAppToGroup   (body = App object)
//   DELETE /api/v0/authz/groups/{group_id}/apps/{app_id} RemoveAllowedAppFromGroup
//   DELETE /api/v0/authz/groups/{group_id}/apps          RemoveAllowedAppsFromGroup
//   GET    /api/v0/authz/apps/{app_id}/groups            GetAllowedGroupsForApp
//   DELETE /api/v0/authz/apps/{app_id}/groups            RemoveAllowedGroupsForApp

const authzBase = "/api/v0/authz"

// addApp is a helper that POSTs an App object directly (proto body: "app").
func addApp(t *testing.T, client *testClient, groupID, appID string) {
	t.Helper()
	// The proto annotation is `body: "app"`, meaning the HTTP body IS the App
	// message itself — just {"client_id": "..."}, not wrapped in {"app": {...}}.
	body := map[string]string{"client_id": appID}
	statusCode, respBody := client.Request(
		http.MethodPost,
		fmt.Sprintf("%s/groups/%s/apps", authzBase, groupID),
		body,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("addApp(%s→%s): expected 200, got %d. Body: %s", groupID, appID, statusCode, string(respBody))
	}
}

// listApps lists allowed apps in a group and returns their client IDs.
func listApps(t *testing.T, client *testClient, groupID string) []string {
	t.Helper()
	statusCode, body := client.Request(
		http.MethodGet,
		fmt.Sprintf("%s/groups/%s/apps", authzBase, groupID),
		nil,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("listApps(%s): expected 200, got %d. Body: %s", groupID, statusCode, string(body))
	}
	var resp struct {
		Data []struct {
			ClientId string `json:"client_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("listApps(%s): failed to unmarshal: %v", groupID, err)
	}
	ids := make([]string, len(resp.Data))
	for i, a := range resp.Data {
		ids[i] = a.ClientId
	}
	return ids
}

// TestGetAllowedAppsInGroup covers GET /groups/{group_id}/apps for an empty group.
func TestGetAllowedAppsInGroup(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("group-%d", time.Now().UnixNano()))

	apps := listApps(t, client, groupID)
	if len(apps) != 0 {
		t.Errorf("expected empty list for new group, got %d apps", len(apps))
	}
}

// TestAddAllowedAppToGroup covers POST /groups/{group_id}/apps.
func TestAddAllowedAppToGroup(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("group-%d", time.Now().UnixNano()))
	appID := fmt.Sprintf("client-%d", time.Now().UnixNano())

	addApp(t, client, groupID, appID)

	apps := listApps(t, client, groupID)
	found := false
	for _, a := range apps {
		if a == appID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("app %s not found after adding to group %s", appID, groupID)
	}
}

// TestAddDuplicateAppToGroup ensures adding the same app twice returns a non-200.
func TestAddDuplicateAppToGroup(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("group-%d", time.Now().UnixNano()))
	appID := fmt.Sprintf("client-dup-%d", time.Now().UnixNano())

	addApp(t, client, groupID, appID) // first add — must succeed

	body := map[string]string{"client_id": appID}
	statusCode, _ := client.Request(
		http.MethodPost,
		fmt.Sprintf("%s/groups/%s/apps", authzBase, groupID),
		body,
	)
	if statusCode == http.StatusOK {
		t.Errorf("expected non-200 on duplicate add, got 200")
	}
}

// TestRemoveAllowedAppFromGroup covers DELETE /groups/{group_id}/apps/{app_id}.
func TestRemoveAllowedAppFromGroup(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("group-%d", time.Now().UnixNano()))
	appID := fmt.Sprintf("client-rem-%d", time.Now().UnixNano())

	addApp(t, client, groupID, appID)

	statusCode, respBody := client.Request(
		http.MethodDelete,
		fmt.Sprintf("%s/groups/%s/apps/%s", authzBase, groupID, appID),
		nil,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected 200 removing app, got %d. Body: %s", statusCode, string(respBody))
	}

	apps := listApps(t, client, groupID)
	for _, a := range apps {
		if a == appID {
			t.Errorf("app %s still present after removal", appID)
		}
	}
}

// TestRemoveAllowedAppsFromGroup covers DELETE /groups/{group_id}/apps (bulk remove).
func TestRemoveAllowedAppsFromGroup(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("group-%d", time.Now().UnixNano()))

	appIDs := []string{
		fmt.Sprintf("client-a-%d", time.Now().UnixNano()),
		fmt.Sprintf("client-b-%d", time.Now().UnixNano()),
	}
	for _, appID := range appIDs {
		addApp(t, client, groupID, appID)
	}

	statusCode, respBody := client.Request(
		http.MethodDelete,
		fmt.Sprintf("%s/groups/%s/apps", authzBase, groupID),
		nil,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected 200 bulk removing apps, got %d. Body: %s", statusCode, string(respBody))
	}

	apps := listApps(t, client, groupID)
	if len(apps) != 0 {
		t.Errorf("expected no apps after bulk removal, got %d", len(apps))
	}
}

// TestGetAllowedGroupsForApp covers GET /apps/{app_id}/groups.
func TestGetAllowedGroupsForApp(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	group1ID := createTestGroup(t, client, fmt.Sprintf("group1-%d", time.Now().UnixNano()))
	group2ID := createTestGroup(t, client, fmt.Sprintf("group2-%d", time.Now().UnixNano()))
	appID := fmt.Sprintf("client-shared-%d", time.Now().UnixNano())

	addApp(t, client, group1ID, appID)
	addApp(t, client, group2ID, appID)

	statusCode, body := client.Request(
		http.MethodGet,
		fmt.Sprintf("%s/apps/%s/groups", authzBase, appID),
		nil,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d. Body: %s", statusCode, string(body))
	}

	var resp struct {
		Data []struct {
			GroupId string `json:"group_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	groupsFound := make(map[string]bool)
	for _, g := range resp.Data {
		groupsFound[g.GroupId] = true
	}
	if !groupsFound[group1ID] {
		t.Errorf("group %s not found for app %s", group1ID, appID)
	}
	if !groupsFound[group2ID] {
		t.Errorf("group %s not found for app %s", group2ID, appID)
	}
}

// TestRemoveAllowedGroupsForApp covers DELETE /apps/{app_id}/groups.
func TestRemoveAllowedGroupsForApp(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("group-%d", time.Now().UnixNano()))
	appID := fmt.Sprintf("client-rga-%d", time.Now().UnixNano())

	addApp(t, client, groupID, appID)

	statusCode, respBody := client.Request(
		http.MethodDelete,
		fmt.Sprintf("%s/apps/%s/groups", authzBase, appID),
		nil,
	)
	if statusCode != http.StatusOK {
		t.Fatalf("expected 200 removing groups for app, got %d. Body: %s", statusCode, string(respBody))
	}

	// Verify no groups remain for the app — accept 200+empty or 404.
	statusCode, listBody := client.Request(
		http.MethodGet,
		fmt.Sprintf("%s/apps/%s/groups", authzBase, appID),
		nil,
	)
	if statusCode != http.StatusOK && statusCode != http.StatusNotFound {
		t.Fatalf("expected 200 or 404 after removal, got %d. Body: %s", statusCode, string(listBody))
	}
	if statusCode == http.StatusOK {
		var resp struct {
			Data []struct {
				GroupId string `json:"group_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(listBody, &resp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if len(resp.Data) != 0 {
			t.Errorf("expected no groups after removal, got %d", len(resp.Data))
		}
	}
}

// TestValidationErrors verifies that malformed / empty IDs return non-200.
func TestValidationErrors(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "GetAllowedAppsInGroup empty group id",
			method: http.MethodGet,
			path:   authzBase + "/groups//apps",
		},
		{
			name:   "GetAllowedGroupsForApp empty app id",
			method: http.MethodGet,
			path:   authzBase + "/apps//groups",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			statusCode, _ := client.Request(tc.method, tc.path, nil)
			if statusCode == http.StatusOK {
				t.Errorf("%s: expected non-200, got 200", tc.name)
			}
		})
	}
}
