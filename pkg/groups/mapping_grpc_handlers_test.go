// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"

	pb "github.com/canonical/hook-service/gen/hook/groups/v1"
	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestMappingGrpcHandler_GetGroupsForUser_Unit(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		tenantID  string
		userID    string
		streamErr error
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:     "stream groups for user with valid tenant",
			tenantID: "tenant-a",
			userID:   "user-1",
			wantErr:  false,
		},
		{
			name:      "stream returns error",
			tenantID:  "tenant-a",
			userID:    "user-1",
			streamErr: ErrInvalidTenant,
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
		},
		{
			name:     "stream groups for user with empty user id",
			tenantID: "tenant-a",
			userID:   "",
			wantErr:  true,
			wantCode: codes.InvalidArgument,
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

			server := NewMappingGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background())).AnyTimes()

			if tt.userID != "" {
				mockSvc.EXPECT().StreamGroupsForUser(gomock.Any(), tt.tenantID, tt.userID, gomock.Any()).DoAndReturn(
					func(ctx context.Context, tenantID, userID string, fn func(*types.Group) error) error {
						if tt.streamErr != nil {
							return tt.streamErr
						}
						return fn(&types.Group{ID: "g1", Name: "group1", TenantId: tenantID, CreatedAt: now, UpdatedAt: now})
					},
				)
			}

			req := &pb.GetGroupsForUserReq{UserId: tt.userID, TenantId: &tt.tenantID}
			stream := &mockGroupMappingServerStream{ctx: context.Background()}

			err := server.GetGroupsForUser(req, stream)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetGroupsForUser() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				s, ok := status.FromError(err)
				if !ok || s.Code() != tt.wantCode {
					t.Errorf("expected code %v, got %v", tt.wantCode, s.Code())
				}
			}
		})
	}
}

func TestMappingGrpcHandler_GetUsersInGroup_Unit(t *testing.T) {
	tests := []struct {
		name      string
		tenantID  string
		groupID   string
		streamErr error
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:     "stream users in group with valid tenant",
			tenantID: "tenant-a",
			groupID:  "group-1",
			wantErr:  false,
		},
		{
			name:      "stream returns error",
			tenantID:  "tenant-a",
			groupID:   "group-1",
			streamErr: ErrStreamInterrupted,
			wantErr:   true,
			wantCode:  codes.Internal,
		},
		{
			name:     "stream users in group with empty group id",
			tenantID: "tenant-a",
			groupID:  "",
			wantErr:  true,
			wantCode: codes.InvalidArgument,
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

			server := NewMappingGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background())).AnyTimes()

			if tt.groupID != "" {
				mockSvc.EXPECT().StreamUsersInGroup(gomock.Any(), tt.tenantID, tt.groupID, gomock.Any()).DoAndReturn(
					func(ctx context.Context, tenantID, groupID string, fn func(string) error) error {
						if tt.streamErr != nil {
							return tt.streamErr
						}
						return fn("user-1")
					},
				)
			}

			req := &pb.GetUsersInGroupReq{GroupId: tt.groupID, TenantId: &tt.tenantID}
			stream := &mockUserMappingServerStream{ctx: context.Background()}

			err := server.GetUsersInGroup(req, stream)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetUsersInGroup() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				s, ok := status.FromError(err)
				if !ok || s.Code() != tt.wantCode {
					t.Errorf("expected code %v, got %v", tt.wantCode, s.Code())
				}
			}
		})
	}
}

type mockGroupMappingServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *mockGroupMappingServerStream) Context() context.Context { return s.ctx }
func (s *mockGroupMappingServerStream) Send(m *pb.GroupMapping) error { return nil }

type mockUserMappingServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *mockUserMappingServerStream) Context() context.Context { return s.ctx }
func (s *mockUserMappingServerStream) Send(m *pb.UserMapping) error { return nil }

func setupMappingIntegrationServer(t *testing.T) (pb.GroupsMappingServiceClient, db.DBClientInterface, func()) {
	t.Helper()

	connStr, pgContainer := setupTestPostgres(t)
	if pgContainer == nil {
		t.Skip("container runtime not available")
	}
	runMigrations(t, connStr)

	logger := logging.NewNoopLogger()
	monitor := monitoring.NewNoopMonitor("hook-service-test", logger)
	tracer := tracing.NewNoopTracer()

	dbClient, err := db.NewDBClient(db.Config{DSN: connStr, MaxConns: 5, MinConns: 1}, tracer, monitor, logger)
	if err != nil {
		pgContainer.Terminate(context.Background())
		t.Fatalf("Failed to create DB client: %v", err)
	}

	s := storage.NewStorage(dbClient, tracer, monitor, logger)
	authz := authorization.NewAuthorizer(
		openfga.NewNoopClient(tracer, monitor, logger),
		tracer, monitor, logger,
	)

	groupSvc := NewService(s, authz, tracer, monitor, logger)
	mappingSrv := NewMappingGrpcServer(groupSvc, tracer, monitor, logger)

	grpcSrv := grpc.NewServer()
	pb.RegisterGroupsMappingServiceServer(grpcSrv, mappingSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		dbClient.Close()
		pgContainer.Terminate(context.Background())
		t.Fatalf("Failed to listen: %v", err)
	}

	go grpcSrv.Serve(lis)

	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", lis.Addr().(*net.TCPAddr).Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcSrv.Stop()
		dbClient.Close()
		pgContainer.Terminate(context.Background())
		t.Fatalf("Failed to dial: %v", err)
	}

	client := pb.NewGroupsMappingServiceClient(conn)

	cleanup := func() {
		conn.Close()
		grpcSrv.GracefulStop()
		dbClient.Close()
		pgContainer.Terminate(context.Background())
	}

	return client, dbClient, cleanup
}

func TestMappingGrpcHandler_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client, _, cleanup := setupMappingIntegrationServer(t)
	defer cleanup()

	ctx := context.Background()
	tenantID := "default"

	t.Run("GetGroupsForUser returns stream for existing user", func(t *testing.T) {
		stream, err := client.GetGroupsForUser(ctx, &pb.GetGroupsForUserReq{
			UserId:   fmt.Sprintf("integ-user-%d@example.com", time.Now().UnixNano()),
			TenantId: &tenantID,
		})
		if err != nil {
			t.Fatalf("GetGroupsForUser failed: %v", err)
		}

		count := 0
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("stream Recv error: %v", err)
			}
			count++
		}
		if count != 0 {
			t.Errorf("expected 0 groups for unknown user, got %d", count)
		}
	})

	t.Run("GetUsersInGroup returns stream for existing group", func(t *testing.T) {
		stream, err := client.GetUsersInGroup(ctx, &pb.GetUsersInGroupReq{
			GroupId:  "00000000-0000-0000-0000-000000000000",
			TenantId: &tenantID,
		})
		if err != nil {
			t.Fatalf("GetUsersInGroup failed: %v", err)
		}

		count := 0
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				st, ok := status.FromError(err)
				if ok && st.Code() == codes.NotFound {
					break
				}
				t.Fatalf("stream Recv error: %v", err)
			}
			count++
		}
		if count != 0 {
			t.Errorf("expected 0 users for non-existent group, got %d", count)
		}
	})

	t.Run("GetGroupsForUser with cross-tenant returns empty", func(t *testing.T) {
		otherTenant := "other-tenant"
		stream, err := client.GetGroupsForUser(ctx, &pb.GetGroupsForUserReq{
			UserId:   "some-user",
			TenantId: &otherTenant,
		})
		if err != nil {
			t.Fatalf("GetGroupsForUser failed: %v", err)
		}

		count := 0
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("stream Recv error: %v", err)
			}
			count++
		}
		if count != 0 {
			t.Errorf("expected 0 groups for cross-tenant, got %d", count)
		}
	})
}

func TestMapMappingErrorToStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		action   string
		wantCode codes.Code
	}{
		{"nil error returns nil", nil, "anything", codes.OK},
		{"ErrInvalidTenant", ErrInvalidTenant, "test", codes.InvalidArgument},
		{"ErrStreamInterrupted", ErrStreamInterrupted, "test", codes.Internal},
		{"ErrUnauthorizedStream", ErrUnauthorizedStream, "test", codes.Unauthenticated},
		{"storage ErrNotFound", storage.ErrNotFound, "test", codes.NotFound},
		{"unknown error", errors.New("boom"), "test-action", codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapMappingErrorToStatus(tt.err, tt.action)
			if tt.err == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			s, ok := status.FromError(result)
			if !ok {
				t.Fatalf("error is not a gRPC status: %v", result)
			}
			if s.Code() != tt.wantCode {
				t.Errorf("expected code %v, got %v", tt.wantCode, s.Code())
			}
		})
	}
}


func TestMappingGrpcHandler_TenantFiltering_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client, dbClient, cleanup := setupMappingIntegrationServer(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert test groups across different tenants
	g1ID := uuid.NewString()
	g2ID := uuid.NewString()

	_, err := dbClient.Statement(ctx).
		Insert("groups").
		Columns("id", "name", "tenant_id", "description", "type", "created_at", "updated_at").
		Values(g1ID, "group-tenant-1", "tenant-1", "desc1", int(types.GroupTypeLocal), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert group 1: %v", err)
	}

	_, err = dbClient.Statement(ctx).
		Insert("groups").
		Columns("id", "name", "tenant_id", "description", "type", "created_at", "updated_at").
		Values(g2ID, "group-tenant-2", "tenant-2", "desc2", int(types.GroupTypeLocal), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert group 2: %v", err)
	}

	// Insert user memberships
	userID := "filtered-user@example.com"
	_, err = dbClient.Statement(ctx).
		Insert("group_members").
		Columns("group_id", "user_id", "tenant_id", "role", "created_at", "updated_at").
		Values(g1ID, userID, "tenant-1", int(types.RoleMember), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert member 1: %v", err)
	}

	_, err = dbClient.Statement(ctx).
		Insert("group_members").
		Columns("group_id", "user_id", "tenant_id", "role", "created_at", "updated_at").
		Values(g2ID, userID, "tenant-2", int(types.RoleMember), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert member 2: %v", err)
	}

	// 1. Test GetGroupsForUser: When tenant_id is provided, it filters by tenant.
	t.Run("GetGroupsForUser with tenant_id filters correctly", func(t *testing.T) {
		tenant1 := "tenant-1"
		stream, err := client.GetGroupsForUser(ctx, &pb.GetGroupsForUserReq{
			UserId:   userID,
			TenantId: &tenant1,
		})
		if err != nil {
			t.Fatalf("GetGroupsForUser failed: %v", err)
		}

		var groups []*pb.GroupMapping
		for {
			g, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Recv failed: %v", err)
			}
			groups = append(groups, g)
		}

		if len(groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(groups))
		}
		if groups[0].Id != g1ID {
			t.Errorf("expected group ID %s, got %s", g1ID, groups[0].Id)
		}
		if groups[0].TenantId != "tenant-1" {
			t.Errorf("expected tenant ID tenant-1, got %s", groups[0].TenantId)
		}
	})

	// 2. Test GetGroupsForUser: When tenant_id is empty/nil, it returns all results across all tenants.
	t.Run("GetGroupsForUser with empty/nil tenant_id returns all across tenants", func(t *testing.T) {
		stream, err := client.GetGroupsForUser(ctx, &pb.GetGroupsForUserReq{
			UserId:   userID,
			TenantId: nil,
		})
		if err != nil {
			t.Fatalf("GetGroupsForUser failed: %v", err)
		}

		var groups []*pb.GroupMapping
		for {
			g, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Recv failed: %v", err)
			}
			groups = append(groups, g)
		}

		if len(groups) != 2 {
			t.Fatalf("expected 2 groups, got %d", len(groups))
		}
	})

	// Setup another user in group-tenant-1 under different tenant ID in group_members to test StreamUsersInGroup
	userTenant1 := "user-tenant-1"
	userTenant2 := "user-tenant-2"

	// Delete old members for g1ID to have a clean slate
	_, _ = dbClient.Statement(ctx).Delete("group_members").Where(sq.Eq{"group_id": g1ID}).ExecContext(ctx)

	_, err = dbClient.Statement(ctx).
		Insert("group_members").
		Columns("group_id", "user_id", "tenant_id", "role", "created_at", "updated_at").
		Values(g1ID, userTenant1, "tenant-1", int(types.RoleMember), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert member 3: %v", err)
	}

	// Insert group 1 under tenant-2 first to satisfy foreign key constraint on group_members
	_, err = dbClient.Statement(ctx).
		Insert("groups").
		Columns("id", "name", "tenant_id", "description", "type", "created_at", "updated_at").
		Values(g1ID, "group-tenant-1-t2", "tenant-2", "desc1", int(types.GroupTypeLocal), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert group 1 under tenant 2: %v", err)
	}

	_, err = dbClient.Statement(ctx).
		Insert("group_members").
		Columns("group_id", "user_id", "tenant_id", "role", "created_at", "updated_at").
		Values(g1ID, userTenant2, "tenant-2", int(types.RoleMember), now, now).
		ExecContext(ctx)
	if err != nil {
		t.Fatalf("failed to insert member 4: %v", err)
	}

	// 3. Test GetUsersInGroup: When tenant_id is provided, it filters by tenant.
	t.Run("GetUsersInGroup with tenant_id filters correctly", func(t *testing.T) {
		tenant1 := "tenant-1"
		stream, err := client.GetUsersInGroup(ctx, &pb.GetUsersInGroupReq{
			GroupId:  g1ID,
			TenantId: &tenant1,
		})
		if err != nil {
			t.Fatalf("GetUsersInGroup failed: %v", err)
		}

		var users []*pb.UserMapping
		for {
			u, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Recv failed: %v", err)
			}
			users = append(users, u)
		}

		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		if users[0].Id != userTenant1 {
			t.Errorf("expected user ID %s, got %s", userTenant1, users[0].Id)
		}
	})

	// 4. Test GetUsersInGroup: When tenant_id is empty/nil, it returns all results across all tenants.
	t.Run("GetUsersInGroup with empty/nil tenant_id returns all across tenants", func(t *testing.T) {
		stream, err := client.GetUsersInGroup(ctx, &pb.GetUsersInGroupReq{
			GroupId:  g1ID,
			TenantId: nil,
		})
		if err != nil {
			t.Fatalf("GetUsersInGroup failed: %v", err)
		}

		var users []*pb.UserMapping
		for {
			u, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Recv failed: %v", err)
			}
			users = append(users, u)
		}

		if len(users) != 2 {
			t.Fatalf("expected 2 users, got %d", len(users))
		}
	})
}
