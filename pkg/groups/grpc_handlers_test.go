// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

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

	"github.com/canonical/hook-service/internal/types"
	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/db"
	httptypes "github.com/canonical/hook-service/internal/http/types"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/migrations"
	authorization_api "github.com/canonical/hook-service/pkg/authorization"
	v0_authz "github.com/canonical/identity-platform-api/v0/authorization"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/protobuf/encoding/protojson"
)

const groupsBase = "/api/v0/authz/groups"
const usersBase = "/api/v0/authz/users"

//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_groups.go -source=./interfaces.go ServiceInterface
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestGrpcHandler_CreateGroup(t *testing.T) {
	now := time.Now()
	strPtr := func(s string) *string { return &s }
	tests := []struct {
		name       string
		input      *v0_groups.CreateGroupReq
		expectResp *types.Group
		expectErr  error
		wantErr    bool
		wantResp   *v0_groups.CreateGroupResp
	}{
		{
			name: "Success",
			input: &v0_groups.CreateGroupReq{
				Group: &v0_groups.GroupInput{
					Name:        "test-group",
					Description: strPtr("A test group"),
					Type:        strPtr("local"),
				},
			},
			expectResp: &types.Group{
				ID:          "group-id",
				Name:        "test-group",
				TenantId:    DefaultTenantID,
				Description: "A test group",
				Type:        types.GroupTypeLocal,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			expectErr: nil,
			wantErr:   false,
			wantResp: &v0_groups.CreateGroupResp{
				Data: []*v0_groups.Group{{
					Id:          "group-id",
					Name:        "test-group",
					TenantId:    DefaultTenantID,
					Description: "A test group",
					Type:        "local",
					CreatedAt:   timestamppb.New(now),
					UpdatedAt:   timestamppb.New(now),
				}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group created"; return &s }(),
			},
		},
		{
			name: "Should succeed without type",
			input: &v0_groups.CreateGroupReq{
				Group: &v0_groups.GroupInput{
					Name:        "test-group",
					Description: strPtr("A test group"),
				},
			},
			expectResp: &types.Group{
				ID:          "group-id",
				Name:        "test-group",
				TenantId:    DefaultTenantID,
				Description: "A test group",
				Type:        types.GroupTypeLocal,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			expectErr: nil,
			wantErr:   false,
			wantResp: &v0_groups.CreateGroupResp{
				Data: []*v0_groups.Group{{
					Id:          "group-id",
					Name:        "test-group",
					TenantId:    DefaultTenantID,
					Description: "A test group",
					Type:        "local",
					CreatedAt:   timestamppb.New(now),
					UpdatedAt:   timestamppb.New(now),
				}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group created"; return &s }(),
			},
		},
		{
			name: "Service returns error",
			input: &v0_groups.CreateGroupReq{
				Group: &v0_groups.GroupInput{
					Name: "test-group",
				},
			},
			expectResp: nil,
			expectErr:  errors.New("service error"),
			wantErr:    true,
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

			gType, _ := types.ParseGroupType(tt.input.Group.GetType())
			g := &types.Group{
				Name:        tt.input.Group.GetName(),
				TenantId:    DefaultTenantID,
				Description: tt.input.Group.GetDescription(),
				Type:        gType,
			}

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.CreateGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().CreateGroup(gomock.Any(), g).Return(tt.expectResp, tt.expectErr)

			resp, err := server.CreateGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("CreateGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("CreateGroup() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcHandler_GetGroup(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		input      *v0_groups.GetGroupReq
		expectResp *types.Group
		expectErr  error
		wantErr    bool
		wantCode   codes.Code
		wantResp   *v0_groups.GetGroupResp
	}{
		{
			name:  "Success",
			input: &v0_groups.GetGroupReq{Id: "group-id"},
			expectResp: &types.Group{
				ID:          "group-id",
				Name:        "test-group",
				TenantId:    DefaultTenantID,
				Description: "A test group",
				Type:        types.GroupTypeLocal,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			expectErr: nil,
			wantErr:   false,
			wantResp: &v0_groups.GetGroupResp{
				Data: []*v0_groups.Group{{
					Id:          "group-id",
					Name:        "test-group",
					TenantId:    DefaultTenantID,
					Description: "A test group",
					Type:        "local",
					CreatedAt:   timestamppb.New(now),
					UpdatedAt:   timestamppb.New(now),
				}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group details"; return &s }(),
			},
		},
		{
			name:       "Group not found",
			input:      &v0_groups.GetGroupReq{Id: "not-found"},
			expectResp: nil,
			expectErr:  ErrGroupNotFound,
			wantErr:    true,
			wantCode:   codes.NotFound,
		},
		{
			name:       "Service returns error",
			input:      &v0_groups.GetGroupReq{Id: "error-id"},
			expectResp: nil,
			expectErr:  errors.New("service error"),
			wantErr:    true,
			wantCode:   codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.GetGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().GetGroup(gomock.Any(), tt.input.Id).Return(tt.expectResp, tt.expectErr)

			resp, err := server.GetGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("GetGroup() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcHandler_ListGroups(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		expectResp []*types.Group
		expectErr  error
		wantErr    bool
		wantCode   codes.Code
		wantResp   *v0_groups.ListGroupsResp
	}{
		{
			name: "Success with groups",
			expectResp: []*types.Group{
				{ID: "group-1", Name: "Group 1", CreatedAt: now, UpdatedAt: now},
				{ID: "group-2", Name: "Group 2", CreatedAt: now, UpdatedAt: now},
			},
			expectErr: nil,
			wantErr:   false,
			wantResp: &v0_groups.ListGroupsResp{
				Data: []*v0_groups.Group{
					{Id: "group-1", Name: "Group 1", Type: "local", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
					{Id: "group-2", Name: "Group 2", Type: "local", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
				},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group list"; return &s }(),
			},
		},
		{
			name:       "Success with no groups",
			expectResp: []*types.Group{},
			expectErr:  nil,
			wantErr:    false,
			wantResp:   &v0_groups.ListGroupsResp{Data: []*v0_groups.Group{}, Status: http.StatusOK, Message: func() *string { s := "Group list"; return &s }()},
		},
		{
			name:       "Service returns error",
			expectResp: nil,
			expectErr:  errors.New("service error"),
			wantErr:    true,
			wantCode:   codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.ListGroups").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().ListGroups(gomock.Any()).Return(tt.expectResp, tt.expectErr)

			resp, err := server.ListGroups(context.Background(), &v0_groups.ListGroupsReq{})

			if (err != nil) != tt.wantErr {
				t.Errorf("ListGroups() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("ListGroups() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcHandler_RemoveGroup(t *testing.T) {
	tests := []struct {
		name      string
		input     *v0_groups.RemoveGroupReq
		expectErr error
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:      "Success",
			input:     &v0_groups.RemoveGroupReq{Id: "group-id"},
			expectErr: nil,
			wantErr:   false,
		},
		{
			name:      "Group not found",
			input:     &v0_groups.RemoveGroupReq{Id: "not-found"},
			expectErr: ErrGroupNotFound,
			wantErr:   true,
			wantCode:  codes.NotFound,
		},
		{
			name:      "Service returns error",
			input:     &v0_groups.RemoveGroupReq{Id: "error-id"},
			expectErr: errors.New("service error"),
			wantErr:   true,
			wantCode:  codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.RemoveGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().DeleteGroup(gomock.Any(), tt.input.Id).Return(tt.expectErr)

			resp, err := server.RemoveGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcHandler_UpdateGroup(t *testing.T) {
	now := time.Now()
	strPtr := func(s string) *string { return &s }
	tests := []struct {
		name       string
		input      *v0_groups.UpdateGroupReq
		expectResp *types.Group
		expectErr  error
		wantErr    bool
		wantCode   codes.Code
		wantResp   *v0_groups.UpdateGroupResp
	}{
		{
			name:       "Success",
			input:      &v0_groups.UpdateGroupReq{Id: "group-id", Group: &v0_groups.GroupInput{Description: strPtr("new description")}},
			expectResp: &types.Group{ID: "group-id", Name: "test-group", Description: "new description", CreatedAt: now, UpdatedAt: now},
			wantErr:    false,
			wantResp: &v0_groups.UpdateGroupResp{
				Data:    []*v0_groups.Group{{Id: "group-id", Name: "test-group", Description: "new description", Type: "local", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group updated"; return &s }(),
			},
		},
		{
			name:      "Group not found",
			input:     &v0_groups.UpdateGroupReq{Id: "not-found", Group: &v0_groups.GroupInput{Description: strPtr("new description")}},
			expectErr: ErrGroupNotFound,
			wantErr:   true,
			wantCode:  codes.NotFound,
		},
		{
			name:      "UpdateGroup returns error",
			input:     &v0_groups.UpdateGroupReq{Id: "group-id", Group: &v0_groups.GroupInput{Description: strPtr("new description")}},
			expectErr: errors.New("update error"),
			wantErr:   true,
			wantCode:  codes.Internal,
		},
		{
			name:     "Updating name is forbidden",
			input:    &v0_groups.UpdateGroupReq{Id: "group-id", Group: &v0_groups.GroupInput{Name: "new-name"}},
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

			server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.UpdateGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().UpdateGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(tt.expectResp, tt.expectErr).AnyTimes()

			resp, err := server.UpdateGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("UpdateGroup() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcHandler_ListUsersInGroup(t *testing.T) {
	tests := []struct {
		name       string
		input      *v0_groups.ListUsersInGroupReq
		expectResp []string
		expectErr  error
		wantErr    bool
		wantCode   codes.Code
		wantResp   *v0_groups.ListUsersInGroupResp
	}{
		{
			name:       "Success with users",
			input:      &v0_groups.ListUsersInGroupReq{Id: "group-id"},
			expectResp: []string{"user-1", "user-2"},
			wantErr:    false,
			wantResp: &v0_groups.ListUsersInGroupResp{
				Data:    []*v0_groups.User{{Id: "user-1"}, {Id: "user-2"}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Users in group"; return &s }(),
			},
		},
		{
			name:       "Success with no users",
			input:      &v0_groups.ListUsersInGroupReq{Id: "group-id"},
			expectResp: []string{},
			wantErr:    false,
			wantResp: &v0_groups.ListUsersInGroupResp{
				Data:    []*v0_groups.User{},
				Status:  http.StatusOK,
				Message: func() *string { s := "Users in group"; return &s }(),
			},
		},
		{
			name:      "Group not found",
			input:     &v0_groups.ListUsersInGroupReq{Id: "not-found"},
			expectErr: ErrGroupNotFound,
			wantErr:   true,
			wantCode:  codes.NotFound,
		},
		{
			name:      "Service returns error",
			input:     &v0_groups.ListUsersInGroupReq{Id: "error-id"},
			expectErr: errors.New("service error"),
			wantErr:   true,
			wantCode:  codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.ListUsersInGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().ListUsersInGroup(gomock.Any(), tt.input.Id).Return(tt.expectResp, tt.expectErr)

			resp, err := server.ListUsersInGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("ListUsersInGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("ListUsersInGroup() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcHandler_AddUsersToGroup(t *testing.T) {
	tests := []struct {
		name      string
		input     *v0_groups.AddUsersToGroupReq
		expectErr error
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:    "Success",
			input:   &v0_groups.AddUsersToGroupReq{Id: "group-id", UserIds: []string{"user-1", "user-2"}},
			wantErr: false,
		},
		{
			name:      "Group not found",
			input:     &v0_groups.AddUsersToGroupReq{Id: "not-found", UserIds: []string{"user-1"}},
			expectErr: ErrGroupNotFound,
			wantErr:   true,
			wantCode:  codes.NotFound,
		},
		{
			name:      "Service returns error",
			input:     &v0_groups.AddUsersToGroupReq{Id: "error-id", UserIds: []string{"user-1"}},
			expectErr: errors.New("service error"),
			wantErr:   true,
			wantCode:  codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.AddUsersToGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().AddUsersToGroup(gomock.Any(), tt.input.Id, tt.input.UserIds).Return(tt.expectErr)

			resp, err := server.AddUsersToGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("AddUsersToGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcHandler_RemoveUserFromGroup(t *testing.T) {
	tests := []struct {
		name      string
		input     *v0_groups.RemoveUserFromGroupReq
		expectErr error
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:    "Success",
			input:   &v0_groups.RemoveUserFromGroupReq{Id: "group-id", UserId: "user-1"},
			wantErr: false,
		},
		{
			name:      "Group not found",
			input:     &v0_groups.RemoveUserFromGroupReq{Id: "not-found", UserId: "user-1"},
			expectErr: ErrGroupNotFound,
			wantErr:   true,
			wantCode:  codes.NotFound,
		},
		{
			name:      "Service returns error",
			input:     &v0_groups.RemoveUserFromGroupReq{Id: "error-id", UserId: "user-1"},
			expectErr: errors.New("service error"),
			wantErr:   true,
			wantCode:  codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.RemoveUserFromGroup").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().RemoveUsersFromGroup(gomock.Any(), tt.input.Id, []string{tt.input.UserId}).Return(tt.expectErr)

			resp, err := server.RemoveUserFromGroup(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveUserFromGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGrpcHandler_ListUserGroups(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		input      *v0_groups.ListUserGroupsReq
		expectResp []*types.Group
		expectErr  error
		wantErr    bool
		wantCode   codes.Code
		wantResp   *v0_groups.ListUserGroupsResp
	}{
		{
			name:  "Success with groups",
			input: &v0_groups.ListUserGroupsReq{Id: "user-1"},
			expectResp: []*types.Group{
				{ID: "group-1", Name: "Group 1", CreatedAt: now, UpdatedAt: now},
				{ID: "group-2", Name: "Group 2", CreatedAt: now, UpdatedAt: now},
			},
			wantErr: false,
			wantResp: &v0_groups.ListUserGroupsResp{
				Data: []*v0_groups.Group{
					{Id: "group-1", Name: "Group 1", Type: "local", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
					{Id: "group-2", Name: "Group 2", Type: "local", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
				},
				Status:  http.StatusOK,
				Message: func() *string { s := "User group list"; return &s }(),
			},
		},
		{
			name:       "Success with no groups",
			input:      &v0_groups.ListUserGroupsReq{Id: "user-2"},
			expectResp: []*types.Group{},
			wantErr:    false,
			wantResp: &v0_groups.ListUserGroupsResp{
				Data:    []*v0_groups.Group{},
				Status:  http.StatusOK,
				Message: func() *string { s := "User group list"; return &s }(),
			},
		},
		{
			name:      "Service returns error",
			input:     &v0_groups.ListUserGroupsReq{Id: "error-id"},
			expectErr: errors.New("service error"),
			wantErr:   true,
			wantCode:  codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.ListUserGroups").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().GetGroupsForUser(gomock.Any(), tt.input.Id).Return(tt.expectResp, tt.expectErr)

			resp, err := server.ListUserGroups(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("ListUserGroups() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.wantResp) {
				t.Errorf("ListUserGroups() resp = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

func TestGrpcHandler_AddUserToGroups(t *testing.T) {
	tests := []struct {
		name      string
		input     *v0_groups.AddUserToGroupsReq
		expectErr error
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:    "Success",
			input:   &v0_groups.AddUserToGroupsReq{Id: "user-1", GroupIds: []string{"group-1", "group-2"}},
			wantErr: false,
		},
		{
			name:      "Group not found",
			input:     &v0_groups.AddUserToGroupsReq{Id: "user-1", GroupIds: []string{"not-found"}},
			expectErr: ErrGroupNotFound,
			wantErr:   true,
			wantCode:  codes.NotFound,
		},
		{
			name:      "Service returns error",
			input:     &v0_groups.AddUserToGroupsReq{Id: "error-id", GroupIds: []string{"group-1"}},
			expectErr: errors.New("service error"),
			wantErr:   true,
			wantCode:  codes.Internal,
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

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockTracer.EXPECT().Start(gomock.Any(), "groups.GrpcServer.AddUserToGroups").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)
			mockSvc.EXPECT().UpdateGroupsForUser(gomock.Any(), tt.input.Id, tt.input.GroupIds).Return(tt.expectErr)

			resp, err := server.AddUserToGroups(context.Background(), tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("AddUserToGroups() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.wantCode {
					t.Errorf("expected gRPC status %v, got %v", tt.wantCode, st.Code())
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestMapErrorToStatus(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		action   string
		wantCode codes.Code
		wantMsg  string
	}{
		{"nil error", nil, "action", codes.OK, ""},
		{"group not found", ErrGroupNotFound, "action", codes.NotFound, "group not found"},
		{"duplicate group", ErrDuplicateGroup, "action", codes.AlreadyExists, "group already exists"},
		{"invalid group name", ErrInvalidGroupName, "action", codes.InvalidArgument, "invalid group name"},
		{"invalid group type", ErrInvalidGroupType, "action", codes.InvalidArgument, "invalid group type"},
		{"invalid tenant", ErrInvalidTenant, "action", codes.InvalidArgument, "invalid tenant"},
		{"unknown error", errors.New("something went wrong"), "test-action", codes.Internal, "test-action failed"},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := NewMockServiceInterface(ctrl)
	mockTracer := NewMockTracingInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)

	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

	server := NewGrpcServer(mockSvc, mockTracer, mockMonitor, mockLogger)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {

			stErr := server.mapErrorToStatus(tc.err, tc.action)
			if tc.err == nil {
				if stErr != nil {
					t.Errorf("expected nil, got %v", stErr)
				}
				return
			}
			s, ok := status.FromError(stErr)
			if !ok {
				t.Fatalf("error is not a gRPC status: %v", stErr)
			}
			if s.Code() != tc.wantCode {
				t.Errorf("expected code %v, got %v", tc.wantCode, s.Code())
			}
			if tc.wantMsg != "" && s.Message() != tc.wantMsg {
				t.Errorf("expected message %q, got %q", tc.wantMsg, s.Message())
			}
		})
	}
}

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
		runtime.WithForwardResponseRewriter(httptypes.ForwardErrorResponseRewriter),
		runtime.WithDisablePathLengthFallback(),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{UseProtoNames: true},
		}),
	)

	authzSvc := authorization_api.NewService(s, authz, tracer, monitor, logger)
	groupSvc := NewService(s, authz, tracer, monitor, logger)

	ctx := context.Background()
	v0_authz.RegisterAppAuthorizationServiceHandlerServer(ctx, gwMux,
		authorization_api.NewGrpcServer(authzSvc, tracer, monitor, logger),
	)
	v0_groups.RegisterAuthzGroupsServiceHandlerServer(ctx, gwMux,
		NewGrpcServer(groupSvc, tracer, monitor, logger),
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

// TestGroupsLifecycle covers POST, GET, PUT, DELETE for groups.
func TestGroupsLifecycle(t *testing.T) {
	t.Parallel()

	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupName := fmt.Sprintf("test-group-%d", time.Now().UnixNano())

	// 1. Create Group
	createBody := map[string]interface{}{
		"name":        groupName,
		"description": "Lifecycle test group",
		"type":        "local",
	}
	statusCode, respBody := client.Request(http.MethodPost, groupsBase, createBody)
	if statusCode != http.StatusOK {
		t.Fatalf("CreateGroup: expected 200, got %d. Body: %s", statusCode, string(respBody))
	}
	var createResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		t.Fatalf("CreateGroup unmarshal error: %v", err)
	}
	if len(createResp.Data) == 0 {
		t.Fatalf("CreateGroup returned empty data")
	}
	groupID := createResp.Data[0].ID

	// 2. Get Group
	statusCode, getBody := client.Request(http.MethodGet, fmt.Sprintf("%s/%s", groupsBase, groupID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("GetGroup: expected 200, got %d. Body: %s", statusCode, string(getBody))
	}
	var getResp struct {
		Data []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(getBody, &getResp); err != nil {
		t.Fatalf("GetGroup unmarshal error: %v", err)
	}
	if len(getResp.Data) == 0 {
		t.Fatalf("GetGroup returned empty data")
	}
	if getResp.Data[0].Name != groupName {
		t.Errorf("GetGroup: expected name %s, got %s", groupName, getResp.Data[0].Name)
	}

	// 3. List Groups
	statusCode, listBody := client.Request(http.MethodGet, groupsBase, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("ListGroups: expected 200, got %d. Body: %s", statusCode, string(listBody))
	}

	// 4. Update Group
	updateBody := map[string]interface{}{
		"description": "Updated description",
		"type":        "local",
	}
	statusCode, updateRespBody := client.Request(http.MethodPut, fmt.Sprintf("%s/%s", groupsBase, groupID), updateBody)
	if statusCode != http.StatusOK {
		t.Fatalf("UpdateGroup: expected 200, got %d. Body: %s", statusCode, string(updateRespBody))
	}

	// Verify update
	statusCode, getUpdatedBody := client.Request(http.MethodGet, fmt.Sprintf("%s/%s", groupsBase, groupID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("GetGroup (after update): expected 200, got %d. Body: %s", statusCode, string(getUpdatedBody))
	}
	if err := json.Unmarshal(getUpdatedBody, &getResp); err != nil {
		t.Fatalf("GetGroup unmarshal error: %v", err)
	}
	if len(getResp.Data) == 0 {
		t.Fatalf("GetGroup returned empty data after update")
	}
	if getResp.Data[0].Description != "Updated description" {
		t.Errorf("expected updated description, got %s", getResp.Data[0].Description)
	}

	// 5. Delete Group
	statusCode, deleteBody := client.Request(http.MethodDelete, fmt.Sprintf("%s/%s", groupsBase, groupID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("DeleteGroup: expected 200, got %d. Body: %s", statusCode, string(deleteBody))
	}

	// Verify delete (GET should fail or return empty/404)
	statusCode, _ = client.Request(http.MethodGet, fmt.Sprintf("%s/%s", groupsBase, groupID), nil)
	if statusCode == http.StatusOK {
		t.Errorf("GetGroup: expected non-200 after deletion, got 200")
	}
}

// TestUserMembership covers adding and removing users from groups.
func TestUserMembership(t *testing.T) {
	t.Parallel()

	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	groupID := createTestGroup(t, client, fmt.Sprintf("um-group-%d", time.Now().UnixNano()))
	userID := fmt.Sprintf("test-user-%d@example.com", time.Now().UnixNano())

	// Add User To Group (POST body is an array of strings mapping to user_ids)
	t.Run("AddUserToGroup", func(t *testing.T) {
		body := []string{userID}
		statusCode, respBody := client.Request(http.MethodPost, fmt.Sprintf("%s/%s/users", groupsBase, groupID), body)
		if statusCode != http.StatusOK {
			t.Fatalf("expected status OK adding user, got %d. Body: %s", statusCode, string(respBody))
		}
	})

	// List Users In Group
	t.Run("ListUsersInGroup", func(t *testing.T) {
		statusCode, body := client.Request(http.MethodGet, fmt.Sprintf("%s/%s/users", groupsBase, groupID), nil)
		if statusCode != http.StatusOK {
			t.Fatalf("expected status OK listing users, got %d. Body: %s", statusCode, string(body))
		}

		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
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
			t.Errorf("added user %s not found in group %s", userID, groupID)
		}
	})

	// List Groups For User
	t.Run("ListGroupsForUser", func(t *testing.T) {
		statusCode, body := client.Request(http.MethodGet, fmt.Sprintf("%s/%s/groups", usersBase, userID), nil)
		if statusCode != http.StatusOK {
			t.Fatalf("expected status OK listing user groups, got %d. Body: %s", statusCode, string(body))
		}

		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
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
			t.Errorf("group %s not found in groups for user %s", groupID, userID)
		}
	})

	// Remove User From Group
	t.Run("RemoveUserFromGroup", func(t *testing.T) {
		statusCode, respBody := client.Request(http.MethodDelete, fmt.Sprintf("%s/%s/users/%s", groupsBase, groupID, userID), nil)
		if statusCode != http.StatusOK {
			t.Fatalf("expected status OK removing user, got %d. Body: %s", statusCode, string(respBody))
		}

		// Verify user is no longer in group
		statusCode, body := client.Request(http.MethodGet, fmt.Sprintf("%s/%s/users", groupsBase, groupID), nil)
		if statusCode != http.StatusOK {
			t.Fatalf("expected status OK listing users after removal, got %d", statusCode)
		}

		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		for _, u := range resp.Data {
			if u.ID == userID {
				t.Errorf("user %s still found in group %s after removal", userID, groupID)
			}
		}
	})
}

// TestAddUserToMultipleGroups verifies AddUsersToGroup works sequentially.
func TestAddUserToMultipleGroups(t *testing.T) {
	t.Parallel()

	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	group1ID := createTestGroup(t, client, fmt.Sprintf("multi-1-%d", time.Now().UnixNano()))
	group2ID := createTestGroup(t, client, fmt.Sprintf("multi-2-%d", time.Now().UnixNano()))

	userID := fmt.Sprintf("multi-group-user-%d@example.com", time.Now().UnixNano())

	// Add user to both groups
	for _, gID := range []string{group1ID, group2ID} {
		body := []string{userID}
		statusCode, respBody := client.Request(http.MethodPost, fmt.Sprintf("%s/%s/users", groupsBase, gID), body)
		if statusCode != http.StatusOK {
			t.Fatalf("expected status OK adding user to group %s, got %d. Body: %s", gID, statusCode, string(respBody))
		}
	}

	// List groups for user
	statusCode, body := client.Request(http.MethodGet, fmt.Sprintf("%s/%s/groups", usersBase, userID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("expected status OK, got %d. Body: %s", statusCode, string(body))
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	groupsFound := make(map[string]bool)
	for _, g := range resp.Data {
		groupsFound[g.ID] = true
	}

	if !groupsFound[group1ID] {
		t.Errorf("group %s not found in user's groups", group1ID)
	}
	if !groupsFound[group2ID] {
		t.Errorf("group %s not found in user's groups", group2ID)
	}
}

// TestSetUserGroups covers PUT /users/{user_id}/groups.
func TestSetUserGroups(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	group1ID := createTestGroup(t, client, fmt.Sprintf("set-1-%d", time.Now().UnixNano()))
	group2ID := createTestGroup(t, client, fmt.Sprintf("set-2-%d", time.Now().UnixNano()))

	userID := fmt.Sprintf("set-groups-user-%d@example.com", time.Now().UnixNano())

	// Use PUT /users/{id}/groups to set groups for a user
	body := []string{group1ID, group2ID}
	statusCode, respBody := client.Request(http.MethodPut, fmt.Sprintf("%s/%s/groups", usersBase, userID), body)
	if statusCode != http.StatusOK {
		t.Fatalf("expected status OK setting user groups, got %d. Body: %s", statusCode, string(respBody))
	}

	// Verify user is in both groups
	statusCode, listBody := client.Request(http.MethodGet, fmt.Sprintf("%s/%s/groups", usersBase, userID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("expected status OK listing user groups, got %d. Body: %s", statusCode, string(listBody))
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listBody, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	groupsFound := make(map[string]bool)
	for _, g := range resp.Data {
		groupsFound[g.ID] = true
	}
	if !groupsFound[group1ID] {
		t.Errorf("group %s not found in user's groups after PUT", group1ID)
	}
	if !groupsFound[group2ID] {
		t.Errorf("group %s not found in user's groups after PUT", group2ID)
	}
}

// TestUserMembershipErrors verifies edge cases with users endpoints.
func TestUserMembershipErrors(t *testing.T) {
	client, teardown := newIntegrationServer(t)
	if client == nil {
		return
	}
	defer teardown()

	t.Run("ListUsersInNonExistentGroup", func(t *testing.T) {
		statusCode, _ := client.Request(http.MethodGet, fmt.Sprintf("%s/non-existent-group/users", groupsBase), nil)
		if statusCode == http.StatusOK {
			t.Error("expected error for listing users in non-existent group, got 200")
		}
	})

	t.Run("AddUserToNonExistentGroup", func(t *testing.T) {
		body := map[string]interface{}{
			"user_ids": []string{"some-user@example.com"},
		}
		statusCode, _ := client.Request(http.MethodPost, fmt.Sprintf("%s/non-existent-group/users", groupsBase), body)
		if statusCode == http.StatusOK {
			t.Error("expected error for adding user to non-existent group, got 200")
		}
	})
}
