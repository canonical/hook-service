// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"context"
	"fmt"
	reflect "reflect"
	"testing"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/salesforce"
	"github.com/canonical/hook-service/internal/types"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_salesforce.go -source=../../internal/salesforce/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go

func TestSalesforceFetchUserGroups(t *testing.T) {
	user := User{SubjectId: "123", Email: "a@a.com"}
	userNoEmail := User{SubjectId: "123"}
	serviceAccount := User{ClientId: "123"}
	err := fmt.Errorf("some error")
	tests := []struct {
		name string

		user             User
		mockedHttpClient func(*MockSalesforceInterface) salesforce.SalesforceInterface

		expectedResult []*types.Group
		expectedError  error
	}{
		{
			name: "should succeed",
			user: user,
			mockedHttpClient: func(c *MockSalesforceInterface) salesforce.SalesforceInterface {
				r := []Record{{Department: "Charmers", Team: "Identity"}}
				q := fmt.Sprintf(query, user.Email)
				c.EXPECT().Query(q, gomock.Any()).Times(1).Return(nil).SetArg(1, r)
				return c
			},
			expectedResult: []*types.Group{{ID: "Charmers", Name: "Charmers"}, {ID: "Identity", Name: "Identity"}},
		},
		{
			name: "user has no email",
			user: userNoEmail,
			mockedHttpClient: func(c *MockSalesforceInterface) salesforce.SalesforceInterface {
				return c
			},
			expectedResult: nil,
		},
		{
			name: "user is a service account",
			user: serviceAccount,
			mockedHttpClient: func(c *MockSalesforceInterface) salesforce.SalesforceInterface {
				return c
			},
			expectedResult: nil,
		},
		{
			name: "user not in Salesforce",
			user: user,
			mockedHttpClient: func(c *MockSalesforceInterface) salesforce.SalesforceInterface {
				r := []Record{}
				c.EXPECT().Query(gomock.Any(), gomock.Any()).Times(1).Return(nil).SetArg(1, r)
				return c
			},
			expectedResult: nil,
		},
		{
			name: "salesforce error",
			user: user,
			mockedHttpClient: func(c *MockSalesforceInterface) salesforce.SalesforceInterface {
				c.EXPECT().Query(gomock.Any(), gomock.Any()).Times(1).Return(err)
				return c
			},
			expectedResult: nil,
			expectedError:  err,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			logger := logging.NewNoopLogger()
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockSalesforce := NewMockSalesforceInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.Salesforce.FetchUserGroups").Times(1).Return(context.TODO(), trace.SpanFromContext(context.TODO()))

			c := Salesforce{
				c:       test.mockedHttpClient(mockSalesforce),
				tracer:  mockTracer,
				monitor: mockMonitor,
				logger:  logger,
			}

			e, err := c.FetchUserGroups(t.Context(), test.user)

			if !reflect.DeepEqual(e, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, e)
			}

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
		})
	}
}
