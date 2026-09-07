// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package groups

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	v1 "github.com/canonical/hook-service/gen/authorization/service/api/v1"
	"github.com/canonical/hook-service/internal/kafka"
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
	org := storage.DefaultTenantID
	description := "A test group"
	groupType := types.GroupTypeLocal
	dbErr := errors.New("db error")

	testCases := []struct {
		name          string
		// ctx is varied per test case to test creator-owner extraction via UserIDFromContext.
		ctx           context.Context
		nilPublisher  bool
		setupMocks    func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface)
		expectedGroup *types.Group
		expectedErr   error
	}{
		{
			name: "success without creator context",
			ctx:  context.Background(),
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
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
			name: "success with creator context publishes owner tuple",
			ctx:  ContextWithUserID(context.Background(), "creator-user-1"),
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *types.Group) (*types.Group, error) {
						g.ID = "new-id-2"
						return g, nil
					},
				).Times(1)
				mockStorage.EXPECT().AddGroupOwner(gomock.Any(), "new-id-2", "creator-user-1").Return(nil).Times(1)
				mockPublisher.EXPECT().PublishWrite(gomock.Any(), "user:creator-user-1", "owner", "group-in-claim:new-id-2").Return(nil).Times(1)
			},
			expectedGroup: &types.Group{
				ID:          "new-id-2",
				Name:        groupName,
				TenantId:    org,
				Description: description,
				Type:        groupType,
			},
			expectedErr: nil,
		},
		{
			name: "success when publisher fails, logs warning",
			ctx:  ContextWithUserID(context.Background(), "creator-user-1"),
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *types.Group) (*types.Group, error) {
						g.ID = "new-id-3"
						return g, nil
					},
				).Times(1)
				mockStorage.EXPECT().AddGroupOwner(gomock.Any(), "new-id-3", "creator-user-1").Return(nil).Times(1)
				mockPublisher.EXPECT().PublishWrite(gomock.Any(), "user:creator-user-1", "owner", "group-in-claim:new-id-3").Return(errors.New("kafka error")).Times(1)
				mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			},
			expectedGroup: &types.Group{
				ID:          "new-id-3",
				Name:        groupName,
				TenantId:    org,
				Description: description,
				Type:        groupType,
			},
			expectedErr: nil,
		},
		{
			name:         "success with creator context and nil publisher",
			ctx:          ContextWithUserID(context.Background(), "creator-user-1"),
			nilPublisher: true,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *types.Group) (*types.Group, error) {
						g.ID = "new-id-nil-pub"
						return g, nil
					},
				).Times(1)
				mockStorage.EXPECT().AddGroupOwner(gomock.Any(), "new-id-nil-pub", "creator-user-1").Return(nil).Times(1)
			},
			expectedGroup: &types.Group{
				ID:          "new-id-nil-pub",
				Name:        groupName,
				TenantId:    org,
				Description: description,
				Type:        groupType,
			},
			expectedErr: nil,
		},
		{
			name: "storage AddGroupOwner error",
			ctx:  ContextWithUserID(context.Background(), "creator-user-1"),
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *types.Group) (*types.Group, error) {
						g.ID = "new-id-4"
						return g, nil
					},
				).Times(1)
				mockStorage.EXPECT().AddGroupOwner(gomock.Any(), "new-id-4", "creator-user-1").Return(dbErr).Times(1)
			},
			expectedGroup: nil,
			expectedErr:   dbErr,
		},
		{
			name: "db error",
			ctx:  context.Background(),
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, dbErr)
			},
			expectedGroup: nil,
			expectedErr:   dbErr,
		},
		{
			name: "duplicate group name",
			ctx:  context.Background(),
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(tc.ctx, trace.SpanFromContext(tc.ctx))
			tc.setupMocks(mockStorage, mockPublisher, mockLogger)

			var publisher PermissionPublisherInterface = mockPublisher
			if tc.nilPublisher {
				publisher = nil
			}
			s := NewService(mockStorage, mockAuthz, publisher, mockTracer, mockMonitor, mockLogger)

			g := &types.Group{
				Name:        groupName,
				TenantId:    org,
				Description: description,
				Type:        groupType,
			}
			createdGroup, err := s.CreateGroup(tc.ctx, g)

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
				mockStorage.EXPECT().GetGroup(gomock.Any(), "not-found").Return(nil, storage.ErrNotFound)
			},
			expectedGroup: nil,
			expectedErr:   ErrGroupNotFound,
		},
		{
			name:    "db error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().GetGroup(gomock.Any(), groupID).Return(nil, dbErr)
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockPublisher, mockTracer, mockMonitor, mockLogger)

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

func TestService_UpdateGroup(t *testing.T) {
	groupID := "test-id"
	groupToUpdate := &types.Group{Name: "updated-group", Description: "updated description"}
	updatedGroup := &types.Group{ID: groupID, Name: "updated-group", Description: "updated description"}
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockPublisher, mockTracer, mockMonitor, mockLogger)

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
		name         string
		nilPublisher bool
		groupID      string
		setupMocks   func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface)
		expectedErr  error
	}{
		{
			name:    "success without publisher cleanup",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
				mockStorage.EXPECT().ListOwnersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:    "success with members and owners from db publishes delete events",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{"user1", "user2"}, nil)
				mockStorage.EXPECT().ListOwnersInGroup(gomock.Any(), groupID).Return([]string{"creator-owner"}, nil)
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockPublisher.EXPECT().PublishOperations(gomock.Any(),
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user1", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user2", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:creator-owner", Relation: "owner", Object: "group-in-claim:" + groupID},
				).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:         "success with members and owners and nil publisher",
			nilPublisher: true,
			groupID:      groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{"user1", "user2"}, nil)
				mockStorage.EXPECT().ListOwnersInGroup(gomock.Any(), groupID).Return([]string{"creator-owner"}, nil)
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:    "success when publisher delete fails, logs warning",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{"user1"}, nil)
				mockStorage.EXPECT().ListOwnersInGroup(gomock.Any(), groupID).Return([]string{"creator-owner"}, nil)
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockPublisher.EXPECT().PublishOperations(gomock.Any(),
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user1", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:creator-owner", Relation: "owner", Object: "group-in-claim:" + groupID},
				).Return(errors.New("kafka error"))
				mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			},
			expectedErr: nil,
		},
		{
			name:    "db error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
				mockStorage.EXPECT().ListOwnersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(dbErr)
			},
			expectedErr: fmt.Errorf("failed to delete group from db: %v", dbErr),
		},
		{
			name:    "authz error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
				mockStorage.EXPECT().ListOwnersInGroup(gomock.Any(), groupID).Return([]string{}, nil)
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			var publisher PermissionPublisherInterface = mockPublisher
			if tc.nilPublisher {
				publisher = nil
			}
			s := NewService(mockStorage, mockAuthz, publisher, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage, mockAuthz, mockPublisher, mockLogger)

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
		name         string
		nilPublisher bool
		setupMocks   func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface)
		expectedErr  error
	}{
		{
			name: "success with permission publisher",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(nil)
				mockPublisher.EXPECT().PublishOperations(gomock.Any(),
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:user1", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:user2", Relation: "member", Object: "group-in-claim:" + groupID},
				).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:         "success with nil publisher",
			nilPublisher: true,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name: "success when publisher fails, logs warning",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(nil)
				mockPublisher.EXPECT().PublishOperations(gomock.Any(),
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:user1", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:user2", Relation: "member", Object: "group-in-claim:" + groupID},
				).Return(errors.New("kafka error"))
				mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			},
			expectedErr: nil,
		},
		{
			name: "invalid group id",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(storage.ErrForeignKeyViolation)
			},
			expectedErr: ErrInvalidGroupID,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			var publisher PermissionPublisherInterface = mockPublisher
			if tc.nilPublisher {
				publisher = nil
			}
			s := NewService(mockStorage, mockAuthz, publisher, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage, mockPublisher, mockLogger)

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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockPublisher, mockTracer, mockMonitor, mockLogger)

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
		name         string
		nilPublisher bool
		setupMocks   func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface)
		expectedErr  error
	}{
		{
			name: "success with publisher delete",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(nil)
				mockPublisher.EXPECT().PublishOperations(gomock.Any(),
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user1", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user2", Relation: "member", Object: "group-in-claim:" + groupID},
				).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name:         "success with nil publisher",
			nilPublisher: true,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(nil)
			},
			expectedErr: nil,
		},
		{
			name: "success when publisher fails, logs warning",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(nil)
				mockPublisher.EXPECT().PublishOperations(gomock.Any(),
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user1", Relation: "member", Object: "group-in-claim:" + groupID},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:user2", Relation: "member", Object: "group-in-claim:" + groupID},
				).Return(errors.New("kafka error"))
				mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			},
			expectedErr: nil,
		},
		{
			name: "not found",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(ErrGroupNotFound)
			},
			expectedErr: ErrGroupNotFound,
		},
		{
			name: "storage not found mapped to ErrGroupNotFound",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(storage.ErrNotFound)
			},
			expectedErr: ErrGroupNotFound,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockPublisher *MockPermissionPublisherInterface, mockLogger *MockLoggerInterface) {
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			var publisher PermissionPublisherInterface = mockPublisher
			if tc.nilPublisher {
				publisher = nil
			}
			s := NewService(mockStorage, mockAuthz, publisher, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage, mockPublisher, mockLogger)

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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockPublisher, mockTracer, mockMonitor, mockLogger)

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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockPublisher, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			err := s.UpdateGroupsForUser(context.Background(), userID, groupIDs)

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

func TestService_ListGroups(t *testing.T) {
	expectedGroups := []*types.Group{{ID: "g1", Name: "group-1"}, {ID: "g2", Name: "group-2"}}
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
			mockPublisher := NewMockPermissionPublisherInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)

			s := NewService(mockStorage, mockAuthz, mockPublisher, mockTracer, mockMonitor, mockLogger)

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background()))
			tc.setupMocks(mockStorage)

			groups, err := s.ListGroups(context.Background())

			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
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
