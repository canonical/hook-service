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
