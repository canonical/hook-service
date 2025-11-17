// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/canonical/hook-service/internal/types"
	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

			gType := types.GroupType(tt.input.Group.GetType())
			if gType == "" {
				gType = types.GroupTypeLocal
			}
			g := &types.Group{
				Name:        tt.input.Group.GetName(),
				TenantId:    DefaultTenantID,
				Description: tt.input.Group.GetDescription(),
				Type:        gType,
			}

			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
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
					{Id: "group-1", Name: "Group 1", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
					{Id: "group-2", Name: "Group 2", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
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
				Data:    []*v0_groups.Group{{Id: "group-id", Name: "test-group", Description: "new description", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}},
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
					{Id: "group-1", Name: "Group 1", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
					{Id: "group-2", Name: "Group 2", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
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
