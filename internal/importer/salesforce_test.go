// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package importer

import (
"context"
"errors"
"testing"

"github.com/canonical/hook-service/internal/salesforce"
"github.com/canonical/hook-service/internal/storage"
"github.com/canonical/hook-service/internal/types"
trace "go.opentelemetry.io/otel/trace"
"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_storage_test.go -source=./salesforce.go StorageInterface
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_salesforce_test.go github.com/canonical/hook-service/internal/salesforce SalesforceInterface
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_logging_test.go github.com/canonical/hook-service/internal/logging LoggerInterface
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_tracing_test.go github.com/canonical/hook-service/internal/tracing TracingInterface
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_monitoring_test.go github.com/canonical/hook-service/internal/monitoring MonitorInterface

func TestSalesforceImporter_ImportUserGroups(t *testing.T) {
dbErr := errors.New("db error")
sfErr := errors.New("salesforce query error")

testCases := []struct {
name          string
setupMocks    func(*MockStorageInterface, *MockSalesforceInterface, *MockLoggerInterface)
expectedCount int
expectedErr   error
}{
{
name: "success with multiple users and groups",
setupMocks: func(mockStorage *MockStorageInterface, mockSF *MockSalesforceInterface, mockLogger *MockLoggerInterface) {
members := []salesforce.TeamMember{
{Email: "user1@example.com", Department: "Engineering", Team: "Backend"},
{Email: "user2@example.com", Department: "Engineering", Team: "Frontend"},
}
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ string, result any) error {
*result.(*[]salesforce.TeamMember) = members
return nil
},
)

// Expect group creation for Engineering
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ context.Context, g *types.Group) (*types.Group, error) {
g.ID = "eng-id"
return g, nil
},
)
// Expect group creation for Backend
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ context.Context, g *types.Group) (*types.Group, error) {
g.ID = "backend-id"
return g, nil
},
)
// Expect group creation for Frontend
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ context.Context, g *types.Group) (*types.Group, error) {
g.ID = "frontend-id"
return g, nil
},
)

// Expect user group updates
mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), "user1@example.com", gomock.Any()).Return(nil)
mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), "user2@example.com", gomock.Any()).Return(nil)
},
expectedCount: 2,
expectedErr:   nil,
},
{
name: "salesforce query error",
setupMocks: func(mockStorage *MockStorageInterface, mockSF *MockSalesforceInterface, mockLogger *MockLoggerInterface) {
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).Return(sfErr)
},
expectedCount: 0,
expectedErr:   sfErr,
},
{
name: "duplicate group handling",
setupMocks: func(mockStorage *MockStorageInterface, mockSF *MockSalesforceInterface, mockLogger *MockLoggerInterface) {
members := []salesforce.TeamMember{
{Email: "user1@example.com", Department: "Engineering", Team: "Backend"},
}
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ string, result any) error {
*result.(*[]salesforce.TeamMember) = members
return nil
},
)

// First group already exists
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, storage.ErrDuplicateKey)
mockLogger.EXPECT().Infof("Group %s already exists, will merge memberships", "Engineering")
// Fetch existing groups to get IDs
mockStorage.EXPECT().ListGroups(gomock.Any()).Return([]*types.Group{
{ID: "eng-id", Name: "Engineering"},
{ID: "backend-id", Name: "Backend"},
}, nil)

// Second group created successfully
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ context.Context, g *types.Group) (*types.Group, error) {
g.ID = "backend-id"
return g, nil
},
)

mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), "user1@example.com", gomock.Any()).Return(nil)
},
expectedCount: 1,
expectedErr:   nil,
},
{
name: "user update failure logged but continues",
setupMocks: func(mockStorage *MockStorageInterface, mockSF *MockSalesforceInterface, mockLogger *MockLoggerInterface) {
members := []salesforce.TeamMember{
{Email: "user1@example.com", Department: "Engineering", Team: "Backend"},
{Email: "user2@example.com", Department: "Sales", Team: ""},
}
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ string, result any) error {
*result.(*[]salesforce.TeamMember) = members
return nil
},
)

// Create groups
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ context.Context, g *types.Group) (*types.Group, error) {
g.ID = g.Name + "-id"
return g, nil
},
).Times(3)

// First user fails
mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), "user1@example.com", gomock.Any()).Return(dbErr)
mockLogger.EXPECT().Warnf(gomock.Any(), "user1@example.com", dbErr)

// Second user succeeds
mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), "user2@example.com", gomock.Any()).Return(nil)
},
expectedCount: 1,
expectedErr:   nil,
},
}

for _, tc := range testCases {
t.Run(tc.name, func(t *testing.T) {
ctrl := gomock.NewController(t)
defer ctrl.Finish()

mockStorage := NewMockStorageInterface(ctrl)
mockTracer := NewMockTracingInterface(ctrl)
mockLogger := NewMockLoggerInterface(ctrl)
mockMonitor := NewMockMonitorInterface(ctrl)
mockSF := NewMockSalesforceInterface(ctrl)

imp := NewSalesforceImporter(mockStorage, mockTracer, mockMonitor, mockLogger)

mockTracer.EXPECT().Start(gomock.Any(), "importer.SalesforceImporter.ImportUserGroups").Return(context.Background(), trace.SpanFromContext(context.Background()))
tc.setupMocks(mockStorage, mockSF, mockLogger)

count, err := imp.ImportUserGroups(context.Background(), mockSF)

if tc.expectedErr != nil {
if err == nil {
t.Fatalf("expected error %v, got nil", tc.expectedErr)
}
} else {
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if count != tc.expectedCount {
t.Fatalf("expected count %d, got %d", tc.expectedCount, count)
}
}
})
}
}
