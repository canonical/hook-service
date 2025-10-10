package hooks

import (
	"context"
	"errors"
	reflect "reflect"
	"testing"

	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestServiceFetchUserGroups(t *testing.T) {
	err := errors.New("some error")
	u := User{SubjectId: "123", Email: "a@a.com"}
	tests := []struct {
		name  string
		input User

		mockedClients func(*gomock.Controller) []ClientInterface

		expectedResult []string
		expectedError  error
	}{
		{
			name:  "Single service",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				return []ClientInterface{mockClient1}
			},
			expectedResult: []string{"g1", "g2"},
		},
		{
			name:  "Single service repeated",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2", "g1"}, nil)
				return []ClientInterface{mockClient1}
			},
			expectedResult: []string{"g1", "g2"},
		},
		{
			name:  "Multiple services",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g3", "g1"}, nil)
				return []ClientInterface{mockClient1, mockClient2}
			},
			expectedResult: []string{"g1", "g2", "g3"},
		},
		{
			name:  "Multiple services with empty result",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{""}, nil)
				mockClient3 := NewMockClientInterface(ctrl)
				mockClient3.EXPECT().FetchUserGroups(gomock.Any(), u).Return(nil, nil)
				return []ClientInterface{mockClient1, mockClient2, mockClient3}
			},
			expectedResult: []string{"g1", "g2"},
		},
		{
			name:  "Multiple services with error",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]string{"g1", "g2"}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return(nil, err)
				mockClient3 := NewMockClientInterface(ctrl)
				return []ClientInterface{mockClient1, mockClient2, mockClient3}
			},
			expectedError: err,
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

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.Service.FetchUserGroups").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService(test.mockedClients(ctrl), mockAuthorizer, mockTracer, mockMonitor, mockLogger)

			groups, err := s.FetchUserGroups(context.TODO(), test.input)

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
			if !reflect.DeepEqual(groups, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, groups)
			}
		})
	}

}

func TestServiceAuthorizeRequest(t *testing.T) {
	err := errors.New("some error")
	user := User{SubjectId: "123", Email: "a@a.com"}
	serviceAccount := User{ClientId: "client_id"}
	tests := []struct {
		name string

		user       User
		clientId   string
		grantTypes []string
		grantedAud []string
		groups     []string

		mockedCanAccess func(*gomock.Controller) AuthorizerInterface

		expectedResult bool
		expectedError  error
	}{
		{
			name:       "User can access the application",
			user:       user,
			clientId:   "client_id",
			grantTypes: []string{"authorization_code"},
			grantedAud: []string{"client_id"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client_id", []string{"g1", "g2"}).Return(true, nil)
				return mockAuthorizer
			},
			expectedResult: true,
		},
		{
			name:       "User cannot access the application",
			user:       user,
			clientId:   "client_id",
			grantTypes: []string{"authorization_code"},
			grantedAud: []string{"client_id"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client_id", []string{"g1", "g2"}).Return(false, nil)
				return mockAuthorizer
			},
			expectedResult: false,
		},
		{
			name:       "Authorization check fails",
			user:       user,
			clientId:   "client_id",
			grantTypes: []string{"authorization_code"},
			grantedAud: []string{"client_id"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client_id", []string{"g1", "g2"}).Return(false, err)
				return mockAuthorizer
			},
			expectedResult: false,
			expectedError:  err,
		},
		{
			name:       "Service account can access the application with single audience",
			user:       serviceAccount,
			clientId:   "client_id",
			grantTypes: []string{"client_credentials"},
			grantedAud: []string{"app"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().BatchCanAccess(gomock.Any(), serviceAccount.GetUserId(), []string{"app"}, []string{"g1", "g2"}).Return(true, nil)
				return mockAuthorizer
			},
			expectedResult: true,
		},
		{
			name:       "Service account cannot access the application with single audience",
			user:       serviceAccount,
			clientId:   "client_id",
			grantTypes: []string{"client_credentials"},
			grantedAud: []string{"app"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().BatchCanAccess(gomock.Any(), serviceAccount.GetUserId(), []string{"app"}, []string{"g1", "g2"}).Return(false, nil)
				return mockAuthorizer
			},
			expectedResult: false,
		},
		{
			name:       "Service account can access the application with multiple audiences",
			user:       serviceAccount,
			clientId:   "client_id",
			grantTypes: []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			grantedAud: []string{"app1", "app2"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().BatchCanAccess(gomock.Any(), serviceAccount.GetUserId(), []string{"app1", "app2"}, []string{"g1", "g2"}).Return(true, nil)
				return mockAuthorizer
			},
			expectedResult: true,
		},
		{
			name:       "Service account cannot access the application with multiple audiences",
			user:       serviceAccount,
			clientId:   "client_id",
			grantTypes: []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			grantedAud: []string{"app1", "app2"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().BatchCanAccess(gomock.Any(), serviceAccount.GetUserId(), []string{"app1", "app2"}, []string{"g1", "g2"}).Return(false, nil)
				return mockAuthorizer
			},
			expectedResult: false,
		},
		{
			name:       "Service account authorization check fails",
			user:       serviceAccount,
			clientId:   "client_id",
			grantTypes: []string{"client_credentials"},
			grantedAud: []string{"app"},
			groups:     []string{"g1", "g2"},
			mockedCanAccess: func(ctrl *gomock.Controller) AuthorizerInterface {
				mockAuthorizer := NewMockAuthorizerInterface(ctrl)
				mockAuthorizer.EXPECT().BatchCanAccess(gomock.Any(), serviceAccount.GetUserId(), []string{"app"}, []string{"g1", "g2"}).Return(false, err)
				return mockAuthorizer
			},
			expectedResult: false,
			expectedError:  err,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockClient := NewMockClientInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.Service.AuthorizeRequest").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService([]ClientInterface{mockClient}, test.mockedCanAccess(ctrl), mockTracer, mockMonitor, mockLogger)

			req := createHookRequest(test.clientId, test.user.SubjectId, test.grantTypes, test.grantedAud)

			allowed, err := s.AuthorizeRequest(context.TODO(), test.user, req, test.groups)

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
			if !reflect.DeepEqual(allowed, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, allowed)
			}
		})
	}

}
