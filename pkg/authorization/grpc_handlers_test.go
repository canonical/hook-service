package authorization

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	v0_authz "github.com/canonical/identity-platform-api/v0/authorization"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_authorization.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//goʻgenerate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestGetAllowedAppsInGroup(t *testing.T) {
	tests := []struct {
		name          string
		groupID       string
		serviceResult []string
		serviceErr    error
		expectErr     bool
		expectedResp  *v0_authz.GetAllowedAppsInGroupResp
	}{
		{
			name:          "Success with apps",
			groupID:       "group1",
			serviceResult: []string{"app1", "app2"},
			serviceErr:    nil,
			expectErr:     false,
			expectedResp: &v0_authz.GetAllowedAppsInGroupResp{
				Data:    []*v0_authz.App{{ClientId: "app1"}, {ClientId: "app2"}},
				Status:  http.StatusOK,
				Message: func() *string { s := "Allowed apps for group"; return &s }(),
			},
		},
		{
			name:          "Success with no apps",
			groupID:       "group2",
			serviceResult: []string{},
			serviceErr:    nil,
			expectErr:     false,
			expectedResp: &v0_authz.GetAllowedAppsInGroupResp{
				Data:    []*v0_authz.App{},
				Status:  http.StatusOK,
				Message: func() *string { s := "Allowed apps for group"; return &s }(),
			},
		},
		{
			name:          "Service returns error",
			groupID:       "group3",
			serviceResult: nil,
			serviceErr:    errors.New("service error"),
			expectErr:     true,
			expectedResp:  nil,
		},
		{
			name:          "Group not found",
			groupID:       "group-not-found",
			serviceResult: nil,
			serviceErr:    errGroupNotFound,
			expectErr:     true,
			expectedResp:  nil,
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
			mockSvc.EXPECT().GetAllowedAppsInGroup(gomock.Any(), tt.groupID).Return(tt.serviceResult, tt.serviceErr)

			req := &v0_authz.GetAllowedAppsInGroupReq{GroupId: tt.groupID}
			resp, err := server.GetAllowedAppsInGroup(context.Background(), req)

			if (err != nil) != tt.expectErr {
				t.Errorf("GetAllowedAppsInGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				// If service returned a sentinel not-found error, ensure gRPC status is NotFound
				if errors.Is(tt.serviceErr, errGroupNotFound) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for group not found, got %v", err)
					}
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("GetAllowedAppsInGroup() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestAddAllowedAppToGroup(t *testing.T) {
	tests := []struct {
		name       string
		groupID    string
		clientID   string
		serviceErr error
		expectErr  bool
	}{
		{
			name:       "Success",
			groupID:    "group1",
			clientID:   "app1",
			serviceErr: nil,
			expectErr:  false,
		},
		{
			name:       "Service returns error",
			groupID:    "group2",
			clientID:   "app2",
			serviceErr: errors.New("service error"),
			expectErr:  true,
		},
		{
			name:       "Group not found",
			groupID:    "group-not-found",
			clientID:   "app3",
			serviceErr: errGroupNotFound,
			expectErr:  true,
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
			mockSvc.EXPECT().AddAllowedAppToGroup(gomock.Any(), tt.groupID, tt.clientID).Return(tt.serviceErr)

			req := &v0_authz.AddAllowedAppToGroupReq{GroupId: tt.groupID, App: &v0_authz.App{ClientId: tt.clientID}}
			resp, err := server.AddAllowedAppToGroup(context.Background(), req)

			if (err != nil) != tt.expectErr {
				t.Errorf("AddAllowedAppToGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				if errors.Is(tt.serviceErr, errGroupNotFound) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for group not found, got %v", err)
					}
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestRemoveAllowedAppFromGroup(t *testing.T) {
	tests := []struct {
		name       string
		groupID    string
		appID      string
		serviceErr error
		expectErr  bool
	}{
		{
			name:       "Success",
			groupID:    "group1",
			appID:      "app1",
			serviceErr: nil,
			expectErr:  false,
		},
		{
			name:       "Service returns error",
			groupID:    "group2",
			appID:      "app2",
			serviceErr: errors.New("service error"),
			expectErr:  true,
		},
		{
			name:       "Group not found",
			groupID:    "group-not-found",
			appID:      "app3",
			serviceErr: errGroupNotFound,
			expectErr:  true,
		},
		{
			name:       "App does not exist in group",
			groupID:    "group1",
			appID:      "app-not-in-group",
			serviceErr: errAppDoesNotExistInGroup,
			expectErr:  true,
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
			mockSvc.EXPECT().RemoveAllowedAppFromGroup(gomock.Any(), tt.groupID, tt.appID).Return(tt.serviceErr)

			req := &v0_authz.RemoveAllowedAppFromGroupReq{GroupId: tt.groupID, AppId: tt.appID}
			resp, err := server.RemoveAllowedAppFromGroup(context.Background(), req)

			if (err != nil) != tt.expectErr {
				t.Errorf("RemoveAllowedAppFromGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				if errors.Is(tt.serviceErr, errGroupNotFound) || errors.Is(tt.serviceErr, errAppDoesNotExistInGroup) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for not found errors, got %v", err)
					}
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestRemoveAllowedAppsFromGroup(t *testing.T) {
	tests := []struct {
		name       string
		groupID    string
		serviceErr error
		expectErr  bool
	}{
		{
			name:       "Success",
			groupID:    "group1",
			serviceErr: nil,
			expectErr:  false,
		},
		{
			name:       "Service returns error",
			groupID:    "group2",
			serviceErr: errors.New("service error"),
			expectErr:  true,
		},
		{
			name:       "Group not found",
			groupID:    "group-not-found",
			serviceErr: errGroupNotFound,
			expectErr:  true,
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
			mockSvc.EXPECT().RemoveAllowedAppsFromGroup(gomock.Any(), tt.groupID).Return(tt.serviceErr)

			req := &v0_authz.RemoveAllowedAppsFromGroupReq{GroupId: tt.groupID}
			resp, err := server.RemoveAllowedAppsFromGroup(context.Background(), req)

			if (err != nil) != tt.expectErr {
				t.Errorf("RemoveAllowedAppsFromGroup() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				if errors.Is(tt.serviceErr, errGroupNotFound) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for group not found, got %v", err)
					}
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}

func TestGetAllowedGroupsForApp(t *testing.T) {
	tests := []struct {
		name          string
		appID         string
		serviceResult []string
		serviceErr    error
		expectErr     bool
		expectedResp  *v0_authz.GetAllowedGroupsForAppResp
	}{
		{
			name:          "Success with groups",
			appID:         "app1",
			serviceResult: []string{"group1", "group2"},
			serviceErr:    nil,
			expectErr:     false,
			expectedResp: &v0_authz.GetAllowedGroupsForAppResp{
				Data:    []*v0_authz.Group{{GroupId: "group1"}, {GroupId: "group2"}},
				Status:  http.StatusOK,
				Message: func() *string { s := "List of groups allowed for app"; return &s }(),
			},
		},
		{
			name:          "Success with no groups",
			appID:         "app2",
			serviceResult: []string{},
			serviceErr:    nil,
			expectErr:     false,
			expectedResp: &v0_authz.GetAllowedGroupsForAppResp{
				Data:    []*v0_authz.Group{},
				Status:  http.StatusOK,
				Message: func() *string { s := "List of groups allowed for app"; return &s }(),
			},
		},
		{
			name:          "Service returns error",
			appID:         "app3",
			serviceResult: nil,
			serviceErr:    errors.New("service error"),
			expectErr:     true,
			expectedResp:  nil,
		},
		{
			name:          "App not found",
			appID:         "app-not-found",
			serviceResult: nil,
			serviceErr:    errAppDoesNotExist,
			expectErr:     true,
			expectedResp:  nil,
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
			mockSvc.EXPECT().GetAllowedGroupsForApp(gomock.Any(), tt.appID).Return(tt.serviceResult, tt.serviceErr)

			req := &v0_authz.GetAllowedGroupsForAppReq{AppId: tt.appID}
			resp, err := server.GetAllowedGroupsForApp(context.Background(), req)

			if (err != nil) != tt.expectErr {
				t.Errorf("GetAllowedGroupsForApp() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				if errors.Is(tt.serviceErr, errAppDoesNotExist) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for app not found, got %v", err)
					}
				}
				return
			}

			if !reflect.DeepEqual(resp, tt.expectedResp) {
				t.Errorf("GetAllowedGroupsForApp() resp = %v, want %v", resp, tt.expectedResp)
			}
		})
	}
}

func TestRemoveAllowedGroupsForApp(t *testing.T) {
	tests := []struct {
		name       string
		appID      string
		serviceErr error
		expectErr  bool
	}{
		{
			name:       "Success",
			appID:      "app1",
			serviceErr: nil,
			expectErr:  false,
		},
		{
			name:       "Service returns error",
			appID:      "app2",
			serviceErr: errors.New("service error"),
			expectErr:  true,
		},
		{
			name:       "App not found",
			appID:      "app-not-found",
			serviceErr: errAppDoesNotExist,
			expectErr:  true,
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
			mockSvc.EXPECT().RemoveAllowedGroupsForApp(gomock.Any(), tt.appID).Return(tt.serviceErr)

			req := &v0_authz.RemoveAllowedGroupsForAppReq{AppId: tt.appID}
			resp, err := server.RemoveAllowedGroupsForApp(context.Background(), req)

			if (err != nil) != tt.expectErr {
				t.Errorf("RemoveAllowedGroupsForApp() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if tt.expectErr {
				if errors.Is(tt.serviceErr, errAppDoesNotExist) {
					st, ok := status.FromError(err)
					if !ok || st.Code() != codes.NotFound {
						t.Errorf("expected gRPC NotFound for app not found, got %v", err)
					}
				}
				return
			}

			if resp.Status != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.Status)
			}
		})
	}
}
