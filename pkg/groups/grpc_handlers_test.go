package groups

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"go.opentelemetry.io/otel/trace"
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
		name         string
		req          *v0_groups.CreateGroupReq
		serviceResp  *Group
		serviceErr   error
		expectErr    bool
		expectedResp *v0_groups.CreateGroupResp
	}{
		{
			name: "Success",
			req: &v0_groups.CreateGroupReq{
				Group: &v0_groups.GroupInput{
					Name:        "test-group",
					Description: strPtr("A test group"),
					Type:        strPtr("local"),
				},
			},
			serviceResp: &Group{
				ID:           "group-id",
				Name:         "test-group",
				Organization: "default",
				Description:  "A test group",
				Type:         GroupTypeLocal,
				CreatedAt:    now,
				UpdatedAt:    now,
			},
			serviceErr: nil,
			expectErr:  false,
			expectedResp: &v0_groups.CreateGroupResp{
				Data: []*v0_groups.Group{{
					Id:           "group-id",
					Name:         "test-group",
					Organization: "default",
					Description:  "A test group",
					Type:         "local",
					CreatedAt:    timestamppb.New(now),
					UpdatedAt:    timestamppb.New(now),
				}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group created"; return &s }(),
			},
		},
		{
			name: "Should succeed without type",
			req: &v0_groups.CreateGroupReq{
				Group: &v0_groups.GroupInput{
					Name:        "test-group",
					Description: strPtr("A test group"),
				},
			},
			serviceResp: &Group{
				ID:           "group-id",
				Name:         "test-group",
				Organization: "default",
				Description:  "A test group",
				Type:         GroupTypeLocal,
				CreatedAt:    now,
				UpdatedAt:    now,
			},
			serviceErr: nil,
			expectErr:  false,
			expectedResp: &v0_groups.CreateGroupResp{
				Data: []*v0_groups.Group{{
					Id:           "group-id",
					Name:         "test-group",
					Organization: "default",
					Description:  "A test group",
					Type:         "local",
					CreatedAt:    timestamppb.New(now),
					UpdatedAt:    timestamppb.New(now),
				}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group created"; return &s }(),
			},
		},
		{
			name: "Service returns error",
			req: &v0_groups.CreateGroupReq{
				Group: &v0_groups.GroupInput{
					Name: "test-group",
				},
			},
			serviceResp: nil,
			serviceErr:  errors.New("service error"),
			expectErr:   true,
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

			gType := groupType(tt.req.Group.GetType())
			if gType == "" {
				gType = GroupTypeLocal
			}

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			mockSvc.EXPECT().CreateGroup(gomock.Any(), tt.req.Group.Name, "default", tt.req.Group.GetDescription(), gType).Return(tt.serviceResp, tt.serviceErr)

			resp, err := server.CreateGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("CreateGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("CreateGroup() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestGrpcHandler_GetGroup(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		req          *v0_groups.GetGroupReq
		serviceResp  *Group
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
		expectedResp *v0_groups.GetGroupResp
	}{
		{
			name: "Success",
			req:  &v0_groups.GetGroupReq{Id: "group-id"},
			serviceResp: &Group{
				ID:           "group-id",
				Name:         "test-group",
				Organization: "default",
				Description:  "A test group",
				Type:         GroupTypeLocal,
				CreatedAt:    now,
				UpdatedAt:    now,
			},
			serviceErr: nil,
			expectErr:  false,
			expectedResp: &v0_groups.GetGroupResp{
				Data: []*v0_groups.Group{{
					Id:           "group-id",
					Name:         "test-group",
					Organization: "default",
					Description:  "A test group",
					Type:         "local",
					CreatedAt:    timestamppb.New(now),
					UpdatedAt:    timestamppb.New(now),
				}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group details"; return &s }(),
			},
		},
		{
			name:         "Group not found",
			req:          &v0_groups.GetGroupReq{Id: "not-found"},
			serviceResp:  nil,
			serviceErr:   ErrGroupNotFound,
			expectErr:    true,
			expectedCode: codes.NotFound,
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.GetGroupReq{Id: "error-id"},
			serviceResp:  nil,
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().GetGroup(gomock.Any(), tt.req.Id).Return(tt.serviceResp, tt.serviceErr)

			resp, err := server.GetGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("GetGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("GetGroup() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestGrpcHandler_ListGroups(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		serviceResp  []*Group
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
		expectedResp *v0_groups.ListGroupsResp
	}{
		{
			name: "Success with groups",
			serviceResp: []*Group{
				{ID: "group-1", Name: "Group 1", CreatedAt: now, UpdatedAt: now},
				{ID: "group-2", Name: "Group 2", CreatedAt: now, UpdatedAt: now},
			},
			serviceErr: nil,
			expectErr:  false,
			expectedResp: &v0_groups.ListGroupsResp{
				Data: []*v0_groups.Group{
					{Id: "group-1", Name: "Group 1", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
					{Id: "group-2", Name: "Group 2", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
				},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group list"; return &s }(),
			},
		},
		{
			name:         "Success with no groups",
			serviceResp:  []*Group{},
			serviceErr:   nil,
			expectErr:    false,
			expectedResp: &v0_groups.ListGroupsResp{Data: []*v0_groups.Group{}, Status: http.StatusOK, Message: func() *string { s := "Group list"; return &s }()},
		},
		{
			name:         "Service returns error",
			serviceResp:  nil,
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().ListGroups(gomock.Any()).Return(tt.serviceResp, tt.serviceErr)

			resp, err := server.ListGroups(context.Background(), &v0_groups.ListGroupsReq{})

			if (err != nil) != tt.expectErr {
				t.Errorf("ListGroups() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("ListGroups() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestGrpcHandler_RemoveGroup(t *testing.T) {
	tests := []struct {
		name         string
		req          *v0_groups.RemoveGroupReq
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
	}{
		{
			name:       "Success",
			req:        &v0_groups.RemoveGroupReq{Id: "group-id"},
			serviceErr: nil,
			expectErr:  false,
		},
		{
			name:         "Group not found",
			req:          &v0_groups.RemoveGroupReq{Id: "not-found"},
			serviceErr:   ErrGroupNotFound,
			expectErr:    true,
			expectedCode: codes.NotFound,
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.RemoveGroupReq{Id: "error-id"},
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().DeleteGroup(gomock.Any(), tt.req.Id).Return(tt.serviceErr)

			resp, err := server.RemoveGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("RemoveGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
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
		name            string
		req             *v0_groups.UpdateGroupReq
		updateGroupResp *Group
		updateGroupErr  error
		expectErr       bool
		expectedCode    codes.Code
		expectedResp    *v0_groups.UpdateGroupResp
	}{
		{
			name:            "Success",
			req:             &v0_groups.UpdateGroupReq{Id: "group-id", Group: &v0_groups.GroupInput{Description: strPtr("new description")}},
			updateGroupResp: &Group{ID: "group-id", Name: "test-group", Description: "new description", CreatedAt: now, UpdatedAt: now},
			expectErr:       false,
			expectedResp: &v0_groups.UpdateGroupResp{
				Data:    []*v0_groups.Group{{Id: "group-id", Name: "test-group", Description: "new description", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Group updated"; return &s }(),
			},
		},
		{
			name:           "Group not found",
			req:            &v0_groups.UpdateGroupReq{Id: "not-found"},
			updateGroupErr: ErrGroupNotFound,
			expectErr:      true,
			expectedCode:   codes.NotFound,
		},
		{
			name:           "UpdateGroup returns error",
			req:            &v0_groups.UpdateGroupReq{Id: "group-id", Group: &v0_groups.GroupInput{Description: strPtr("new description")}},
			updateGroupErr: errors.New("update error"),
			expectErr:      true,
			expectedCode:   codes.Internal,
		},
		{
			name:         "Updating name is forbidden",
			req:          &v0_groups.UpdateGroupReq{Id: "group-id", Group: &v0_groups.GroupInput{Name: "new-name"}},
			expectErr:    true,
			expectedCode: codes.InvalidArgument,
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
			mockSvc.EXPECT().UpdateGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(tt.updateGroupResp, tt.updateGroupErr).AnyTimes()

			resp, err := server.UpdateGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("UpdateGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("UpdateGroup() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestGrpcHandler_ListUsersInGroup(t *testing.T) {
	tests := []struct {
		name         string
		req          *v0_groups.ListUsersInGroupReq
		serviceResp  []string
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
		expectedResp *v0_groups.ListUsersInGroupResp
	}{
		{
			name:        "Success with users",
			req:         &v0_groups.ListUsersInGroupReq{Id: "group-id"},
			serviceResp: []string{"user-1", "user-2"},
			expectErr:   false,
			expectedResp: &v0_groups.ListUsersInGroupResp{
				Data:    []*v0_groups.User{{Id: "user-1"}, {Id: "user-2"}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Users in group"; return &s }(),
			},
		},
		{
			name:        "Success with no users",
			req:         &v0_groups.ListUsersInGroupReq{Id: "group-id"},
			serviceResp: []string{},
			expectErr:   false,
			expectedResp: &v0_groups.ListUsersInGroupResp{
				Data:    []*v0_groups.User{},
				Status:  http.StatusOK,
				Message: func() *string { s := "Users in group"; return &s }(),
			},
		},
		{
			name:         "Group not found",
			req:          &v0_groups.ListUsersInGroupReq{Id: "not-found"},
			serviceErr:   ErrGroupNotFound,
			expectErr:    true,
			expectedCode: codes.NotFound,
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.ListUsersInGroupReq{Id: "error-id"},
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().ListUsersInGroup(gomock.Any(), tt.req.Id).Return(tt.serviceResp, tt.serviceErr)

			resp, err := server.ListUsersInGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("ListUsersInGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("ListUsersInGroup() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestGrpcHandler_AddUsersToGroup(t *testing.T) {
	tests := []struct {
		name         string
		req          *v0_groups.AddUsersToGroupReq
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
	}{
		{
			name:      "Success",
			req:       &v0_groups.AddUsersToGroupReq{Id: "group-id", UserIds: []string{"user-1", "user-2"}},
			expectErr: false,
		},
		{
			name:         "Group not found",
			req:          &v0_groups.AddUsersToGroupReq{Id: "not-found", UserIds: []string{"user-1"}},
			serviceErr:   ErrGroupNotFound,
			expectErr:    true,
			expectedCode: codes.NotFound,
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.AddUsersToGroupReq{Id: "error-id", UserIds: []string{"user-1"}},
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().AddUsersToGroup(gomock.Any(), tt.req.Id, tt.req.UserIds).Return(tt.serviceErr)

			resp, err := server.AddUsersToGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("AddUsersToGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
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
		name         string
		req          *v0_groups.RemoveUserFromGroupReq
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
	}{
		{
			name:      "Success",
			req:       &v0_groups.RemoveUserFromGroupReq{Id: "group-id", UserId: "user-1"},
			expectErr: false,
		},
		{
			name:         "Group not found",
			req:          &v0_groups.RemoveUserFromGroupReq{Id: "not-found", UserId: "user-1"},
			serviceErr:   ErrGroupNotFound,
			expectErr:    true,
			expectedCode: codes.NotFound,
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.RemoveUserFromGroupReq{Id: "error-id", UserId: "user-1"},
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().RemoveUsersFromGroup(gomock.Any(), tt.req.Id, []string{tt.req.UserId}).Return(tt.serviceErr)

			resp, err := server.RemoveUserFromGroup(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("RemoveUserFromGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
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
		name         string
		req          *v0_groups.ListUserGroupsReq
		serviceResp  []*Group
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
		expectedResp *v0_groups.ListUserGroupsResp
	}{
		{
			name: "Success with groups",
			req:  &v0_groups.ListUserGroupsReq{Id: "user-1"},
			serviceResp: []*Group{
				{ID: "group-1", Name: "Group 1", CreatedAt: now, UpdatedAt: now},
				{ID: "group-2", Name: "Group 2", CreatedAt: now, UpdatedAt: now},
			},
			expectErr: false,
			expectedResp: &v0_groups.ListUserGroupsResp{
				Data: []*v0_groups.Group{
					{Id: "group-1", Name: "Group 1", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
					{Id: "group-2", Name: "Group 2", CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
				},
				Status:  http.StatusOK,
				Message: func() *string { s := "User group list"; return &s }(),
			},
		},
		{
			name:        "Success with no groups",
			req:         &v0_groups.ListUserGroupsReq{Id: "user-2"},
			serviceResp: []*Group{},
			expectErr:   false,
			expectedResp: &v0_groups.ListUserGroupsResp{
				Data:    []*v0_groups.Group{},
				Status:  http.StatusOK,
				Message: func() *string { s := "User group list"; return &s }(),
			},
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.ListUserGroupsReq{Id: "error-id"},
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().GetGroupsForUser(gomock.Any(), tt.req.Id).Return(tt.serviceResp, tt.serviceErr)

			resp, err := server.ListUserGroups(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("ListUserGroups() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("ListUserGroups() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestGrpcHandler_AddUserToGroups(t *testing.T) {
	tests := []struct {
		name         string
		req          *v0_groups.AddUserToGroupsReq
		serviceErr   error
		expectErr    bool
		expectedCode codes.Code
	}{
		{
			name:      "Success",
			req:       &v0_groups.AddUserToGroupsReq{Id: "user-1", GroupIds: []string{"group-1", "group-2"}},
			expectErr: false,
		},
		{
			name:         "Group not found",
			req:          &v0_groups.AddUserToGroupsReq{Id: "user-1", GroupIds: []string{"not-found"}},
			serviceErr:   ErrGroupNotFound,
			expectErr:    true,
			expectedCode: codes.NotFound,
		},
		{
			name:         "Service returns error",
			req:          &v0_groups.AddUserToGroupsReq{Id: "error-id", GroupIds: []string{"group-1"}},
			serviceErr:   errors.New("service error"),
			expectErr:    true,
			expectedCode: codes.Internal,
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
			mockSvc.EXPECT().UpdateGroupsForUser(gomock.Any(), tt.req.Id, tt.req.GroupIds).Return(tt.serviceErr)

			resp, err := server.AddUserToGroups(context.Background(), tt.req)

			if (err != nil) != tt.expectErr {
				t.Errorf("AddUserToGroups() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				st, ok := status.FromError(err)
				if !ok || st.Code() != tt.expectedCode {
					t.Errorf("expected gRPC status %v, got %v", tt.expectedCode, st.Code())
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
		{"invalid organization", ErrInvalidOrganization, "action", codes.InvalidArgument, "invalid organization"},
		{"unknown error", errors.New("something went wrong"), "test-action", codes.Internal, "test-action: something went wrong"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stErr := mapErrorToStatus(tc.err, tc.action)
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
