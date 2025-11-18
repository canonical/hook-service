// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/types"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func TestStorageHookGroupsClient_FetchUserGroups(t *testing.T) {
	user := User{SubjectId: "123", Email: "a@a.com"}
	userNoEmail := User{SubjectId: "123"}
	serviceAccount := User{ClientId: "123"}
	err := fmt.Errorf("database error")

	group1 := &types.Group{ID: "group-1", Name: "Engineering"}
	group2 := &types.Group{ID: "group-2", Name: "Platform"}

	tests := []struct {
		name           string
		user           User
		mockedDatabase func(*MockDatabaseInterface) DatabaseInterface
		expectedResult []*types.Group
		expectedError  error
	}{
		{
			name: "should succeed",
			user: user,
			mockedDatabase: func(db *MockDatabaseInterface) DatabaseInterface {
				groups := []*types.Group{group1, group2}
				db.EXPECT().GetGroupsForUser(gomock.Any(), user.Email).Times(1).Return(groups, nil)
				return db
			},
			expectedResult: []*types.Group{group1, group2},
		},
		{
			name: "user has no email",
			user: userNoEmail,
			mockedDatabase: func(db *MockDatabaseInterface) DatabaseInterface {
				// No expectation - function returns early when userId is empty
				return db
			},
			expectedResult: nil,
		},
		{
			name: "user is a service account",
			user: serviceAccount,
			mockedDatabase: func(db *MockDatabaseInterface) DatabaseInterface {
				db.EXPECT().GetGroupsForUser(gomock.Any(), serviceAccount.ClientId).Times(1).Return(nil, nil)
				return db
			},
			expectedResult: nil,
		},
		{
			name: "user not in database",
			user: user,
			mockedDatabase: func(db *MockDatabaseInterface) DatabaseInterface {
				db.EXPECT().GetGroupsForUser(gomock.Any(), user.Email).Times(1).Return([]*types.Group{}, nil)
				return db
			},
			expectedResult: []*types.Group{},
		},
		{
			name: "database error",
			user: user,
			mockedDatabase: func(db *MockDatabaseInterface) DatabaseInterface {
				db.EXPECT().GetGroupsForUser(gomock.Any(), user.Email).Times(1).Return(nil, err)
				return db
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
			mockDatabase := NewMockDatabaseInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.StorageHookGroupsClient.FetchUserGroups").Times(1).Return(t.Context(), trace.SpanFromContext(t.Context()))

			c := StorageHookGroupsClient{
				db:      test.mockedDatabase(mockDatabase),
				tracer:  mockTracer,
				monitor: mockMonitor,
				logger:  logger,
			}

			result, err := c.FetchUserGroups(t.Context(), test.user)

			if !reflect.DeepEqual(result, test.expectedResult) {
				t.Fatalf("expected return value to be %v not %v", test.expectedResult, result)
			}

			if err != test.expectedError {
				t.Fatalf("expected error to be %v not %v", test.expectedError, err)
			}
		})
	}
}
