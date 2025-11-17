package authorization

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/canonical/hook-service/internal/storage"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_authorization.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authorization -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestService_GetAllowedAppsInGroup(t *testing.T) {
	err := errors.New("some db error")
	tests := []struct {
		name           string
		groupID        string
		mockDB         func(ctrl *gomock.Controller, groupID string) AuthorizationDatabaseInterface
		expectedResult []string
		expectedError  error
	}{
		{
			name:    "Success - apps found",
			groupID: "group1",
			mockDB: func(ctrl *gomock.Controller, groupID string) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().GetAllowedApps(gomock.Any(), groupID).Return([]string{"app1", "app2"}, nil)
				return mock
			},
			expectedResult: []string{"app1", "app2"},
			expectedError:  nil,
		},
		{
			name:    "Success - no apps found",
			groupID: "group2",
			mockDB: func(ctrl *gomock.Controller, groupID string) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().GetAllowedApps(gomock.Any(), groupID).Return([]string{}, nil)
				return mock
			},
			expectedResult: []string{},
			expectedError:  nil,
		},
		{
			name:    "DB error",
			groupID: "group3",
			mockDB: func(ctrl *gomock.Controller, groupID string) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().GetAllowedApps(gomock.Any(), groupID).Return(nil, err)
				return mock
			},
			expectedResult: nil,
			expectedError:  err,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			if test.expectedError == nil {
				mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any())
			}
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockAuthorizer := NewMockAuthorizerInterface(ctrl)
			mockDB := test.mockDB(ctrl, test.groupID)

			mockTracer.EXPECT().Start(gomock.Any(), "authorization.Service.GetAllowedAppsInGroup").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(mockDB, mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			apps, err := s.GetAllowedAppsInGroup(context.TODO(), test.groupID)

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
			if !reflect.DeepEqual(apps, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, apps)
			}
		})
	}
}

func TestService_AddAllowedAppToGroup(t *testing.T) {
	dbErr := errors.New("some db error")
	authzErr := errors.New("some authz error")
	groupID := "group1"
	app := "app1"

	tests := []struct {
		name string

		mockDB         func(ctrl *gomock.Controller) AuthorizationDatabaseInterface
		mockAuthorizer func(ctrl *gomock.Controller) AuthorizerInterface

		expectedError error
	}{
		{
			name: "Success",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().AddAllowedApp(gomock.Any(), groupID, app).Return(nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().AddAllowedAppToGroup(gomock.Any(), groupID, app).Return(nil)
				return mock
			},
			expectedError: nil,
		},
		{
			name: "DB error on AddAllowedApp",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().AddAllowedApp(gomock.Any(), groupID, app).Return(dbErr)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: dbErr,
		},
		{
			name: "Duplicate key - app already exists in group",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().AddAllowedApp(gomock.Any(), groupID, app).Return(storage.ErrDuplicateKey)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: ErrAppAlreadyExistsInGroup,
		},
		{
			name: "Foreign key violation - invalid group ID",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().AddAllowedApp(gomock.Any(), groupID, app).Return(storage.ErrForeignKeyViolation)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: ErrInvalidGroupID,
		},
		{
			name: "Authorizer error - no rollback (transaction middleware handles it)",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().AddAllowedApp(gomock.Any(), groupID, app).Return(nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().AddAllowedAppToGroup(gomock.Any(), groupID, app).Return(authzErr)
				return mock
			},
			expectedError: authzErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockAuthorizer := test.mockAuthorizer(ctrl)
			mockDB := test.mockDB(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "authorization.Service.AddAllowedAppToGroup").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(mockDB, mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			err := s.AddAllowedAppToGroup(context.TODO(), groupID, app)

			if test.expectedError != nil {
				if err == nil || err.Error() != test.expectedError.Error() {
					t.Fatalf("expected error %q, got %v", test.expectedError.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_RemoveAllAllowedAppsFromGroup(t *testing.T) {
	dbErr := errors.New("some db error")
	authzErr := errors.New("some authz error")
	groupID := "group1"
	apps := []string{"app1", "app2"}

	tests := []struct {
		name string

		mockDB         func(ctrl *gomock.Controller) AuthorizationDatabaseInterface
		mockAuthorizer func(ctrl *gomock.Controller) AuthorizerInterface

		expectedError error
	}{
		{
			name: "Success",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllowedApps(gomock.Any(), groupID).Return(apps, nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedAppsFromGroup(gomock.Any(), groupID).Return(nil)
				return mock
			},
			expectedError: nil,
		},
		{
			name: "DB error on RemoveAllowedApps",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllowedApps(gomock.Any(), groupID).Return(nil, dbErr)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: dbErr,
		},
		{
			name: "Authorizer error - no rollback (transaction middleware handles it)",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllowedApps(gomock.Any(), groupID).Return(apps, nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedAppsFromGroup(gomock.Any(), groupID).Return(authzErr)
				return mock
			},
			expectedError: authzErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockAuthorizer := test.mockAuthorizer(ctrl)
			mockDB := test.mockDB(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "authorization.Service.RemoveAllAllowedAppsFromGroup").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(mockDB, mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			err := s.RemoveAllAllowedAppsFromGroup(context.TODO(), groupID)

			if test.expectedError != nil {
				if err == nil || err.Error() != test.expectedError.Error() {
					t.Fatalf("expected error %q, got %v", test.expectedError.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_RemoveAllowedAppFromGroup(t *testing.T) {
	dbErr := errors.New("some db error")
	authzErr := errors.New("some authz error")
	groupID := "group1"
	app := "app1"

	tests := []struct {
		name string

		mockDB         func(ctrl *gomock.Controller) AuthorizationDatabaseInterface
		mockAuthorizer func(ctrl *gomock.Controller) AuthorizerInterface

		expectedError error
	}{
		{
			name: "Success",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllowedApp(gomock.Any(), groupID, app).Return(nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().RemoveAllowedAppFromGroup(gomock.Any(), groupID, app).Return(nil)
				return mock
			},
			expectedError: nil,
		},
		{
			name: "DB error on RemoveAllowedApp",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllowedApp(gomock.Any(), groupID, app).Return(dbErr)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: dbErr,
		},
		{
			name: "Authorizer error - no rollback (transaction middleware handles it)",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllowedApp(gomock.Any(), groupID, app).Return(nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().RemoveAllowedAppFromGroup(gomock.Any(), groupID, app).Return(authzErr)
				return mock
			},
			expectedError: authzErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockAuthorizer := test.mockAuthorizer(ctrl)
			mockDB := test.mockDB(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "authorization.Service.RemoveAllowedAppFromGroup").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(mockDB, mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			err := s.RemoveAllowedAppFromGroup(context.TODO(), groupID, app)

			if test.expectedError != nil {
				if err == nil || err.Error() != test.expectedError.Error() {
					t.Fatalf("expected error %q, got %v", test.expectedError.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_GetAllowedGroupsForApp(t *testing.T) {
	dbErr := errors.New("some db error")
	app := "app1"

	tests := []struct {
		name           string
		mockDB         func(ctrl *gomock.Controller) AuthorizationDatabaseInterface
		expectedResult []string
		expectedError  error
	}{
		{
			name: "Success",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().GetAllowedGroupsForApp(gomock.Any(), app).Return([]string{"g1", "g2"}, nil)
				return mock
			},
			expectedResult: []string{"g1", "g2"},
			expectedError:  nil,
		},
		{
			name: "DB error",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().GetAllowedGroupsForApp(gomock.Any(), app).Return(nil, dbErr)
				return mock
			},
			expectedResult: nil,
			expectedError:  dbErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockAuthorizer := NewMockAuthorizerInterface(ctrl)
			mockDB := test.mockDB(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "authorization.Service.GetAllowedGroupsForApp").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(mockDB, mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			groups, err := s.GetAllowedGroupsForApp(context.TODO(), app)

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
			if !reflect.DeepEqual(groups, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, groups)
			}
		})
	}
}

func TestService_RemoveAllAllowedGroupsForApp(t *testing.T) {
	dbErr := errors.New("some db error")
	authzErr := errors.New("some authz error")
	app := "app1"
	groups := []string{"g1", "g2"}

	tests := []struct {
		name           string
		mockDB         func(ctrl *gomock.Controller) AuthorizationDatabaseInterface
		mockAuthorizer func(ctrl *gomock.Controller) AuthorizerInterface
		expectedError  error
	}{
		{
			name: "Success",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), app).Return(groups, nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), app).Return(nil)
				return mock
			},
			expectedError: nil,
		},
		{
			name: "DB error on GetAllowedGroupsForApp",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), app).Return(nil, dbErr)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: dbErr,
		},
		{
			name: "DB error on RemoveAllowedApp",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), app).Return(nil, dbErr)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			expectedError: dbErr,
		},
		{
			name: "Authorizer error - no rollback (transaction middleware handles it)",
			mockDB: func(ctrl *gomock.Controller) AuthorizationDatabaseInterface {
				mock := NewMockAuthorizationDatabaseInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), app).Return(groups, nil)
				return mock
			},
			mockAuthorizer: func(ctrl *gomock.Controller) AuthorizerInterface {
				mock := NewMockAuthorizerInterface(ctrl)
				mock.EXPECT().RemoveAllAllowedGroupsForApp(gomock.Any(), app).Return(authzErr)
				return mock
			},
			expectedError: authzErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockAuthorizer := test.mockAuthorizer(ctrl)
			mockDB := test.mockDB(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "authorization.Service.RemoveAllAllowedGroupsForApp").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(mockDB, mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			err := s.RemoveAllAllowedGroupsForApp(context.TODO(), app)

			if test.expectedError != nil {
				if err == nil || err.Error() != test.expectedError.Error() {
					t.Fatalf("expected error %q, got %v", test.expectedError.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
