// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"context"
	"errors"
	reflect "reflect"
	"sync"
	"testing"

	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/types"
	"github.com/google/uuid"
	"github.com/ory/hydra/v2/oauth2"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_pool.go -source=../../internal/pool/interfaces.go

// setupMockSubmit configures a MockWorkerPoolInterface to execute submitted
// commands synchronously inline, push the result to the provided channel, and
// call wg.Done(). This allows ProcessRequest tests to run without a real pool.
func setupMockSubmit(wp *MockWorkerPoolInterface) {
	key := uuid.New()
	wp.EXPECT().Submit(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Do(
		func(command any, results chan *pool.Result[any], wg *sync.WaitGroup) {
			var value any = true
			switch commandFunc := command.(type) {
			case func():
				commandFunc()
			case func() any:
				value = commandFunc()
			}
			results <- pool.NewResult[any](key, value)
			wg.Done()
		},
	).Return(key.String(), nil)
}

func TestServiceFetchUserGroups(t *testing.T) {
	err := errors.New("some error")
	u := User{SubjectId: "123", Email: "a@a.com"}
	tests := []struct {
		name  string
		input User

		mockedClients func(*gomock.Controller) []ClientInterface

		expectedResult []*types.Group
		expectedError  error
	}{
		{
			name:  "Single service",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: "g1"}, {Name: "g2"}}, nil)
				return []ClientInterface{mockClient1}
			},
			expectedResult: []*types.Group{{Name: "g1"}, {Name: "g2"}},
		},
		{
			name:  "Single service repeated",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: "g1"}, {Name: "g2"}, {Name: "g1"}}, nil)
				return []ClientInterface{mockClient1}
			},
			expectedResult: []*types.Group{{Name: "g1"}, {Name: "g2"}, {Name: "g1"}},
		},
		{
			name:  "Multiple services",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: "g1"}, {Name: "g2"}}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: "g3"}, {Name: "g1"}}, nil)
				return []ClientInterface{mockClient1, mockClient2}
			},
			expectedResult: []*types.Group{{Name: "g1"}, {Name: "g2"}, {Name: "g3"}, {Name: "g1"}},
		},
		{
			name:  "Multiple services with empty result",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: "g1"}, {Name: "g2"}}, nil)
				mockClient2 := NewMockClientInterface(ctrl)
				mockClient2.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: ""}}, nil)
				mockClient3 := NewMockClientInterface(ctrl)
				mockClient3.EXPECT().FetchUserGroups(gomock.Any(), u).Return(nil, nil)
				return []ClientInterface{mockClient1, mockClient2, mockClient3}
			},
			expectedResult: []*types.Group{{Name: "g1"}, {Name: "g2"}, {Name: ""}},
		},
		{
			name:  "Multiple services with error",
			input: u,
			mockedClients: func(ctrl *gomock.Controller) []ClientInterface {
				mockClient1 := NewMockClientInterface(ctrl)
				mockClient1.EXPECT().FetchUserGroups(gomock.Any(), u).Return([]*types.Group{{Name: "g1"}, {Name: "g2"}}, nil)
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

			s := NewService(test.mockedClients(ctrl), mockAuthorizer, nil, nil, mockTracer, mockMonitor, mockLogger)

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
		groups     []*types.Group

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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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
			groups:     []*types.Group{{ID: "g1", Name: "g1"}, {ID: "g2", Name: "g2"}},
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

			groupIDs := make([]string, len(test.groups))
			for i, g := range test.groups {
				groupIDs[i] = g.ID
			}

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockClient := NewMockClientInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.Service.AuthorizeRequest").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			s := NewService([]ClientInterface{mockClient}, test.mockedCanAccess(ctrl), nil, nil, mockTracer, mockMonitor, mockLogger)

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

func TestServiceProcessRequest(t *testing.T) {
	someErr := errors.New("some error")
	user := User{SubjectId: "user-123", Email: "a@a.com"}

	groups := []*types.Group{{ID: "g1", Name: "g1"}}

	newService := func(ctrl *gomock.Controller, mockClient ClientInterface, mockAuthz AuthorizerInterface, mockTV TenantValidatorInterface, mockPool pool.WorkerPoolInterface) *Service {
		mockTracer := NewMockTracingInterface(ctrl)
		mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).AnyTimes().Return(context.TODO(), trace.SpanFromContext(context.TODO()))
		mockMonitor := NewMockMonitorInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)
		mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
		return NewService([]ClientInterface{mockClient}, mockAuthz, mockTV, mockPool, mockTracer, mockMonitor, mockLogger)
	}

	tests := []struct {
		name string

		req oauth2.TokenHookRequest

		mockClient func(*gomock.Controller) ClientInterface
		mockAuthz  func(*gomock.Controller) AuthorizerInterface
		mockTV     func(*gomock.Controller) TenantValidatorInterface
		mockPool   func(*gomock.Controller) pool.WorkerPoolInterface

		expectedResult *HookContext
		expectedError  error
		expectedErrIs  error
	}{
		{
			name: "groups fetched, no tenant, authorized",
			req:  createHookRequest("client", user.SubjectId, []string{"authorization_code"}, nil),
			mockClient: func(ctrl *gomock.Controller) ClientInterface {
				m := NewMockClientInterface(ctrl)
				m.EXPECT().FetchUserGroups(gomock.Any(), user).Return(groups, nil)
				return m
			},
			mockAuthz: func(ctrl *gomock.Controller) AuthorizerInterface {
				m := NewMockAuthorizerInterface(ctrl)
				m.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client", []string{"g1"}).Return(true, nil)
				return m
			},
			mockTV: func(ctrl *gomock.Controller) TenantValidatorInterface {
				return NewMockTenantValidatorInterface(ctrl)
			},
			mockPool: func(ctrl *gomock.Controller) pool.WorkerPoolInterface {
				m := NewMockWorkerPoolInterface(ctrl)
				setupMockSubmit(m)
				return m
			},
			expectedResult: &HookContext{Groups: groups},
		},
		{
			name: "groups fetched, tenant member, authorized",
			req:  createHookRequestWithExtra("client", user.SubjectId, []string{"authorization_code"}, nil, map[string]interface{}{"_tenant_id": "t-1"}),
			mockClient: func(ctrl *gomock.Controller) ClientInterface {
				m := NewMockClientInterface(ctrl)
				m.EXPECT().FetchUserGroups(gomock.Any(), user).Return(groups, nil)
				return m
			},
			mockAuthz: func(ctrl *gomock.Controller) AuthorizerInterface {
				m := NewMockAuthorizerInterface(ctrl)
				m.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client", []string{"g1"}).Return(true, nil)
				return m
			},
			mockTV: func(ctrl *gomock.Controller) TenantValidatorInterface {
				m := NewMockTenantValidatorInterface(ctrl)
				m.EXPECT().ValidateMembership(gomock.Any(), user.SubjectId, "t-1").Return(nil)
				return m
			},
			mockPool: func(ctrl *gomock.Controller) pool.WorkerPoolInterface {
				m := NewMockWorkerPoolInterface(ctrl)
				setupMockSubmit(m)
				return m
			},
			expectedResult: &HookContext{Groups: groups, TenantID: "t-1"},
		},
		{
			name: "tenant not member — error returned",
			req:  createHookRequestWithExtra("client", user.SubjectId, []string{"authorization_code"}, nil, map[string]interface{}{"_tenant_id": "t-1"}),
			mockClient: func(ctrl *gomock.Controller) ClientInterface {
				m := NewMockClientInterface(ctrl)
				m.EXPECT().FetchUserGroups(gomock.Any(), user).Return(groups, nil)
				return m
			},
			mockAuthz: func(ctrl *gomock.Controller) AuthorizerInterface {
				m := NewMockAuthorizerInterface(ctrl)
				m.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client", []string{"g1"}).Return(true, nil)
				return m
			},
			mockTV: func(ctrl *gomock.Controller) TenantValidatorInterface {
				m := NewMockTenantValidatorInterface(ctrl)
				m.EXPECT().ValidateMembership(gomock.Any(), user.SubjectId, "t-1").Return(tenants.ErrNotMember)
				return m
			},
			mockPool: func(ctrl *gomock.Controller) pool.WorkerPoolInterface {
				m := NewMockWorkerPoolInterface(ctrl)
				setupMockSubmit(m)
				return m
			},
			expectedErrIs: tenants.ErrNotMember,
		},
		{
			name: "tenant service error — errTenantInternal returned",
			req:  createHookRequestWithExtra("client", user.SubjectId, []string{"authorization_code"}, nil, map[string]interface{}{"_tenant_id": "t-1"}),
			mockClient: func(ctrl *gomock.Controller) ClientInterface {
				m := NewMockClientInterface(ctrl)
				m.EXPECT().FetchUserGroups(gomock.Any(), user).Return(groups, nil)
				return m
			},
			mockAuthz: func(ctrl *gomock.Controller) AuthorizerInterface {
				m := NewMockAuthorizerInterface(ctrl)
				m.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client", []string{"g1"}).Return(true, nil)
				return m
			},
			mockTV: func(ctrl *gomock.Controller) TenantValidatorInterface {
				m := NewMockTenantValidatorInterface(ctrl)
				m.EXPECT().ValidateMembership(gomock.Any(), user.SubjectId, "t-1").Return(errors.New("connection refused"))
				return m
			},
			mockPool: func(ctrl *gomock.Controller) pool.WorkerPoolInterface {
				m := NewMockWorkerPoolInterface(ctrl)
				setupMockSubmit(m)
				return m
			},
			expectedErrIs: errTenantInternal,
		},
		{
			name: "groups fetch error — error returned",
			req:  createHookRequest("client", user.SubjectId, []string{"authorization_code"}, nil),
			mockClient: func(ctrl *gomock.Controller) ClientInterface {
				m := NewMockClientInterface(ctrl)
				m.EXPECT().FetchUserGroups(gomock.Any(), user).Return(nil, someErr)
				return m
			},
			mockAuthz: func(ctrl *gomock.Controller) AuthorizerInterface {
				return NewMockAuthorizerInterface(ctrl)
			},
			mockTV: func(ctrl *gomock.Controller) TenantValidatorInterface {
				return NewMockTenantValidatorInterface(ctrl)
			},
			mockPool: func(ctrl *gomock.Controller) pool.WorkerPoolInterface {
				m := NewMockWorkerPoolInterface(ctrl)
				setupMockSubmit(m)
				return m
			},
			expectedError: errors.New("cannot fetch user groups: some error"),
		},
		{
			name: "access denied — error returned",
			req:  createHookRequest("client", user.SubjectId, []string{"authorization_code"}, nil),
			mockClient: func(ctrl *gomock.Controller) ClientInterface {
				m := NewMockClientInterface(ctrl)
				m.EXPECT().FetchUserGroups(gomock.Any(), user).Return(groups, nil)
				return m
			},
			mockAuthz: func(ctrl *gomock.Controller) AuthorizerInterface {
				m := NewMockAuthorizerInterface(ctrl)
				m.EXPECT().CanAccess(gomock.Any(), user.GetUserId(), "client", []string{"g1"}).Return(false, nil)
				return m
			},
			mockTV: func(ctrl *gomock.Controller) TenantValidatorInterface {
				return NewMockTenantValidatorInterface(ctrl)
			},
			mockPool: func(ctrl *gomock.Controller) pool.WorkerPoolInterface {
				m := NewMockWorkerPoolInterface(ctrl)
				setupMockSubmit(m)
				return m
			},
			expectedError: errors.New("access denied for user user-123 to client client"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			s := newService(ctrl, test.mockClient(ctrl), test.mockAuthz(ctrl), test.mockTV(ctrl), test.mockPool(ctrl))

			result, err := s.ProcessRequest(context.TODO(), user, test.req)

			if test.expectedError != nil {
				if err == nil {
					t.Fatalf("expected error %q, got nil", test.expectedError)
				}
				if err.Error() != test.expectedError.Error() {
					t.Fatalf("expected error %q, got %q", test.expectedError, err)
				}
				return
			}
			if test.expectedErrIs != nil {
				if err == nil {
					t.Fatalf("expected error wrapping %v, got nil", test.expectedErrIs)
				}
				if !errors.Is(err, test.expectedErrIs) {
					t.Fatalf("expected error wrapping %v, got %v", test.expectedErrIs, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !reflect.DeepEqual(result.Groups, test.expectedResult.Groups) {
				t.Fatalf("expected groups %v, got %v", test.expectedResult.Groups, result.Groups)
			}
			if result.TenantID != test.expectedResult.TenantID {
				t.Fatalf("expected TenantID %q, got %q", test.expectedResult.TenantID, result.TenantID)
			}
		})
	}
}
