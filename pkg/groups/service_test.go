// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/types"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_groups.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestService_CreateGroup(t *testing.T) {
	groupName := "test-group"
	org := DefaultTenantID
	description := "A test group"
	groupType := types.GroupTypeLocal
	dbErr := errors.New("db error")

	testCases := []struct {
		name          string
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedGroup *types.Group
		expectedErr   error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *types.Group) (*types.Group, error) {
						if g.Name != groupName {
							t.Fatalf("expected group name %q, got %q", groupName, g.Name)
						}
						if g.TenantId != org {
							t.Fatalf("expected tenant %q, got %q", org, g.TenantId)
						}
						if g.Description != description {
							t.Fatalf("expected description %q, got %q", description, g.Description)
						}
						if g.Type != groupType {
							t.Fatalf("expected group type %v, got %v", groupType, g.Type)
						}
						g.ID = "new-id"
						g.CreatedAt = time.Now()
						g.UpdatedAt = time.Now()
						return g, nil
					},
				).Times(1)
			},
			expectedGroup: &types.Group{
				ID:          "new-id",
				Name:        groupName,
				TenantId:    org,
				Description: description,
				Type:        groupType,
			},
			expectedErr: nil,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, dbErr)
			},
			expectedGroup: nil,
			expectedErr:   dbErr,
		},
		{
			name: "duplicate group name",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, storage.ErrDuplicateKey)
			},
			expectedGroup: nil,
			expectedErr:   ErrDuplicateGroup,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			g := &types.Group{
				Name:        groupName,
				TenantId:    org,
				Description: description,
				Type:        groupType,
			}
			createdGroup, err := s.CreateGroup(context.Background(), g)

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				if createdGroup != nil {
					t.Fatalf("expected createdGroup to be nil, got %+v", createdGroup)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if createdGroup == nil {
					t.Fatalf("expected createdGroup not nil")
				}
				if tc.expectedGroup.Name != createdGroup.Name {
					t.Fatalf("expected group name %q, got %q", tc.expectedGroup.Name, createdGroup.Name)
				}
				if createdGroup.ID == "" {
					t.Fatalf("expected createdGroup ID to be set")
				}
			}
		})
	}
}

func TestService_GetGroup(t *testing.T) {
	groupID := "test-id"
	expectedGroup := &types.Group{ID: groupID, Name: "test-group"}
	dbErr := errors.New("db error")

	testCases := []struct {
		name          string
		groupID       string
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedGroup *types.Group
		expectedErr   error
	}{
		{
			name:    "success",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroup(gomock.Any(), groupID).Return(expectedGroup, nil)
			},
			expectedGroup: expectedGroup,
			expectedErr:   nil,
		},
		{
			name:    "not found",
			groupID: "not-found",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroup(gomock.Any(), "not-found").Return(nil, ErrGroupNotFound)
			},
			expectedGroup: nil,
			expectedErr:   ErrGroupNotFound,
		},
		{
			name:    "db error",
			groupID: "db-error-id",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroup(gomock.Any(), "db-error-id").Return(nil, dbErr)
			},
			expectedGroup: nil,
			expectedErr:   dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			group, err := s.GetGroup(context.Background(), tc.groupID)

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				if group != nil {
					t.Fatalf("expected group to be nil, got %+v", group)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tc.expectedGroup, group) {
					t.Fatalf("expected group %+v, got %+v", tc.expectedGroup, group)
				}
			}
		})
	}
}

func TestService_ListGroups(t *testing.T) {
	expectedGroups := []*types.Group{{ID: "1", Name: "group1"}, {ID: "2", Name: "group2"}}
	dbErr := errors.New("db error")

	testCases := []struct {
		name           string
		setupMocks     func(mockStorage *MockDatabaseInterface)
		expectedGroups []*types.Group
		expectedErr    error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListGroups(gomock.Any()).Return(expectedGroups, nil)
			},
			expectedGroups: expectedGroups,
			expectedErr:    nil,
		},
		{
			name: "success empty",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListGroups(gomock.Any()).Return([]*types.Group{}, nil)
			},
			expectedGroups: []*types.Group{},
			expectedErr:    nil,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListGroups(gomock.Any()).Return(nil, dbErr)
			},
			expectedGroups: nil,
			expectedErr:    dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			groups, err := s.ListGroups(context.Background())

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				if groups != nil {
					t.Fatalf("expected groups to be nil, got %+v", groups)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tc.expectedGroups, groups) {
					t.Fatalf("expected groups %+v, got %+v", tc.expectedGroups, groups)
				}
			}
		})
	}
}

func TestService_UpdateGroup(t *testing.T) {
	groupID := "test-id"
	groupToUpdate := &types.Group{Name: "updated-name"}
	updatedGroup := &types.Group{ID: groupID, Name: "updated-name"}
	dbErr := errors.New("db error")

	testCases := []struct {
		name          string
		groupID       string
		group         *types.Group
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedGroup *types.Group
		expectedErr   error
	}{
		{
			name:    "success",
			groupID: groupID,
			group:   groupToUpdate,
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroup(gomock.Any(), groupID, groupToUpdate).Return(updatedGroup, nil)
			},
			expectedGroup: updatedGroup,
			expectedErr:   nil,
		},
		{
			name:    "not found",
			groupID: "not-found",
			group:   groupToUpdate,
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroup(gomock.Any(), "not-found", groupToUpdate).Return(nil, ErrGroupNotFound)
			},
			expectedGroup: nil,
			expectedErr:   ErrGroupNotFound,
		},
		{
			name:    "db error",
			groupID: groupID,
			group:   groupToUpdate,
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroup(gomock.Any(), groupID, groupToUpdate).Return(nil, dbErr)
			},
			expectedGroup: nil,
			expectedErr:   dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			group, err := s.UpdateGroup(context.Background(), tc.groupID, tc.group)

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				if group != nil {
					t.Fatalf("expected group to be nil, got %+v", group)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tc.expectedGroup, group) {
					t.Fatalf("expected group %+v, got %+v", tc.expectedGroup, group)
				}
			}
		})
	}
}

func TestService_DeleteGroup(t *testing.T) {
	groupID := "test-id"
	dbErr := errors.New("db error")
	authzErr := errors.New("authz error")

	testCases := []struct {
		name        string
		groupID     string
		setupMocks  func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface)
		expectedErr error
	}{
		{
			name:    "success",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface) {
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:    "db error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface) {
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(dbErr)
			},
			expectedErr: fmt.Errorf("failed to delete group from db: %v", dbErr),
		},
		{
			name:    "authz error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface) {
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(authzErr)
			},
			expectedErr: fmt.Errorf("failed to delete group from authz: %v", authzErr),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage, mockAuthz)

			err := s.DeleteGroup(context.Background(), tc.groupID)

			if tc.expectedErr != nil {
				if err == nil || err.Error() != tc.expectedErr.Error() {
					t.Fatalf("expected error %q, got %v", tc.expectedErr.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_AddUsersToGroup(t *testing.T) {
	groupID := "group-id"
	userIDs := []string{"user1", "user2"}
	dbErr := errors.New("db error")

	testCases := []struct {
		name        string
		setupMocks  func(mockStorage *MockDatabaseInterface)
		expectedErr error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name: "invalid group id",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(storage.ErrForeignKeyViolation)
			},
			expectedErr: ErrInvalidGroupID,
		},
		{
			name: "user already in group",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(storage.ErrDuplicateKey)
			},
			expectedErr: ErrUserAlreadyInGroup,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(dbErr)
			},
			expectedErr: fmt.Errorf("failed to add users to group: %v", dbErr),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			err := s.AddUsersToGroup(context.Background(), groupID, userIDs)

			if tc.expectedErr != nil {
				if err == nil || err.Error() != tc.expectedErr.Error() {
					t.Fatalf("expected error %q, got %v", tc.expectedErr.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_ListUsersInGroup(t *testing.T) {
	groupID := "group-id"
	expectedUsers := []string{"user1", "user2"}
	dbErr := errors.New("db error")

	testCases := []struct {
		name          string
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedUsers []string
		expectedErr   error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return(expectedUsers, nil)
			},
			expectedUsers: expectedUsers,
			expectedErr:   nil,
		},
		{
			name: "success empty",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
			},
			expectedUsers: []string{},
			expectedErr:   nil,
		},
		{
			name: "not found",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return(nil, ErrGroupNotFound)
			},
			expectedUsers: nil,
			expectedErr:   ErrGroupNotFound,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return(nil, dbErr)
			},
			expectedUsers: nil,
			expectedErr:   dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			users, err := s.ListUsersInGroup(context.Background(), groupID)

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				if users != nil {
					t.Fatalf("expected users to be nil, got %+v", users)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tc.expectedUsers, users) {
					t.Fatalf("expected users %+v, got %+v", tc.expectedUsers, users)
				}
			}
		})
	}
}

func TestService_RemoveUsersFromGroup(t *testing.T) {
	groupID := "group-id"
	userIDs := []string{"user1", "user2"}
	dbErr := errors.New("db error")

	testCases := []struct {
		name        string
		setupMocks  func(mockStorage *MockDatabaseInterface)
		expectedErr error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name: "not found",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(ErrGroupNotFound)
			},
			expectedErr: ErrGroupNotFound,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(dbErr)
			},
			expectedErr: dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			err := s.RemoveUsersFromGroup(context.Background(), groupID, userIDs)

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_GetGroupsForUser(t *testing.T) {
	userID := "user-id"
	expectedGroups := []*types.Group{{ID: "group1"}, {ID: "group2"}}
	dbErr := errors.New("db error")

	testCases := []struct {
		name           string
		setupMocks     func(mockStorage *MockDatabaseInterface)
		expectedGroups []*types.Group
		expectedErr    error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroupsForUser(gomock.Any(), userID).Return(expectedGroups, nil)
			},
			expectedGroups: expectedGroups,
			expectedErr:    nil,
		},
		{
			name: "success empty",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroupsForUser(gomock.Any(), userID).Return([]*types.Group{}, nil)
			},
			expectedGroups: []*types.Group{},
			expectedErr:    nil,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroupsForUser(gomock.Any(), userID).Return(nil, dbErr)
			},
			expectedGroups: nil,
			expectedErr:    dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			groups, err := s.GetGroupsForUser(context.Background(), userID)

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				if groups != nil {
					t.Fatalf("expected groups to be nil, got %+v", groups)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tc.expectedGroups, groups) {
					t.Fatalf("expected groups %+v, got %+v", tc.expectedGroups, groups)
				}
			}
		})
	}
}

func TestService_UpdateGroupsForUser(t *testing.T) {
	userID := "user-id"
	groupIDs := []string{"group1", "group2"}
	dbErr := errors.New("db error")

	testCases := []struct {
		name        string
		setupMocks  func(mockStorage *MockDatabaseInterface)
		expectedErr error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), userID, groupIDs).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name: "invalid group id - foreign key violation",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), userID, groupIDs).Return(storage.ErrForeignKeyViolation)
			},
			expectedErr: ErrInvalidGroupID,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), userID, groupIDs).Return(dbErr)
			},
			expectedErr: dbErr,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStorage := NewMockDatabaseInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			err := s.UpdateGroupsForUser(context.Background(), userID, groupIDs)

			if tc.expectedErr != nil {
				if err == nil || err.Error() != tc.expectedErr.Error() {
					t.Fatalf("expected error %q, got %v", tc.expectedErr.Error(), err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestService_ImportUserGroupsFromSalesforce(t *testing.T) {
dbErr := errors.New("db error")
sfErr := errors.New("salesforce query error")

testCases := []struct {
name               string
setupMocks         func(*MockDatabaseInterface, *MockSalesforceClientInterface, *MockLoggerInterface)
expectedCount      int
expectedErr        error
}{
{
name: "success with multiple users and groups",
setupMocks: func(mockStorage *MockDatabaseInterface, mockSF *MockSalesforceClientInterface, mockLogger *MockLoggerInterface) {
members := []SalesforceTeamMember{
{Email: "user1@example.com", Department: "Engineering", Team: "Backend"},
{Email: "user2@example.com", Department: "Engineering", Team: "Frontend"},
}
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ string, result any) error {
*result.(*[]SalesforceTeamMember) = members
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
setupMocks: func(mockStorage *MockDatabaseInterface, mockSF *MockSalesforceClientInterface, mockLogger *MockLoggerInterface) {
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).Return(sfErr)
},
expectedCount: 0,
expectedErr:   sfErr,
},
{
name: "duplicate group handling",
setupMocks: func(mockStorage *MockDatabaseInterface, mockSF *MockSalesforceClientInterface, mockLogger *MockLoggerInterface) {
members := []SalesforceTeamMember{
{Email: "user1@example.com", Department: "Engineering", Team: "Backend"},
}
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ string, result any) error {
*result.(*[]SalesforceTeamMember) = members
return nil
},
)

// First group already exists
mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, storage.ErrDuplicateKey)
mockStorage.EXPECT().ListGroups(gomock.Any()).Return([]*types.Group{
{ID: "eng-id", Name: "Engineering"},
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
setupMocks: func(mockStorage *MockDatabaseInterface, mockSF *MockSalesforceClientInterface, mockLogger *MockLoggerInterface) {
members := []SalesforceTeamMember{
{Email: "user1@example.com", Department: "Engineering", Team: "Backend"},
{Email: "user2@example.com", Department: "Sales", Team: ""},
}
mockSF.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
func(_ string, result any) error {
*result.(*[]SalesforceTeamMember) = members
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

mockStorage := NewMockDatabaseInterface(ctrl)
mockAuthz := NewMockAuthorizerInterface(ctrl)
mockTracer := NewMockTracingInterface(ctrl)
mockLogger := NewMockLoggerInterface(ctrl)
mockMonitor := NewMockMonitorInterface(ctrl)
mockSF := NewMockSalesforceClientInterface(ctrl)

s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

mockTracer.EXPECT().Start(gomock.Any(), "groups.Service.ImportUserGroupsFromSalesforce").Return(context.Background(), trace.SpanFromContext(context.Background()))
tc.setupMocks(mockStorage, mockSF, mockLogger)

count, err := s.ImportUserGroupsFromSalesforce(context.Background(), mockSF)

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
