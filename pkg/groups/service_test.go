package groups

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_groups.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package groups -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestService_CreateGroup(t *testing.T) {
	groupName := "test-group"
	org := "default"
	description := "A test group"
	groupType := GroupTypeLocal
	dbErr := errors.New("db error")

	testCases := []struct {
		name          string
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedGroup *Group
		expectedErr   error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *Group) (*Group, error) {
						require.Equal(t, groupName, g.Name)
						require.Equal(t, org, g.Organization)
						require.Equal(t, description, g.Description)
						require.Equal(t, groupType, g.Type)
						g.ID = "new-id"
						g.CreatedAt = time.Now()
						g.UpdatedAt = time.Now()
						return g, nil
					},
				).Times(1)
			},
			expectedGroup: &Group{
				ID:           "new-id",
				Name:         groupName,
				Organization: org,
				Description:  description,
				Type:         groupType,
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

			tc.setupMocks(mockStorage)

			s := NewService(mockStorage, mockAuthz, mockTracer, mockMonitor, mockLogger)

			createdGroup, err := s.CreateGroup(context.Background(), groupName, org, description, groupType)

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, createdGroup)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, createdGroup)
				assert.Equal(t, tc.expectedGroup.Name, createdGroup.Name)
				assert.NotEmpty(t, createdGroup.ID)
			}
		})
	}
}

func TestService_GetGroup(t *testing.T) {
	groupID := "test-id"
	expectedGroup := &Group{ID: groupID, Name: "test-group"}
	dbErr := errors.New("db error")
	notFoundErr := NewGroupNotFoundError("id", "")

	testCases := []struct {
		name          string
		groupID       string
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedGroup *Group
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
				mockStorage.EXPECT().GetGroup(gomock.Any(), "not-found").Return(nil, notFoundErr)
			},
			expectedGroup: nil,
			expectedErr:   notFoundErr,
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

			tc.setupMocks(mockStorage)

			group, err := s.GetGroup(context.Background(), tc.groupID)

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, group)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedGroup, group)
			}
		})
	}
}

func TestService_ListGroups(t *testing.T) {
	expectedGroups := []*Group{{ID: "1", Name: "group1"}, {ID: "2", Name: "group2"}}
	dbErr := errors.New("db error")

	testCases := []struct {
		name           string
		setupMocks     func(mockStorage *MockDatabaseInterface)
		expectedGroups []*Group
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
				mockStorage.EXPECT().ListGroups(gomock.Any()).Return([]*Group{}, nil)
			},
			expectedGroups: []*Group{},
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
			tc.setupMocks(mockStorage)

			groups, err := s.ListGroups(context.Background())

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, groups)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedGroups, groups)
			}
		})
	}
}

func TestService_UpdateGroup(t *testing.T) {
	groupID := "test-id"
	groupToUpdate := &Group{Name: "updated-name"}
	updatedGroup := &Group{ID: groupID, Name: "updated-name"}
	dbErr := errors.New("db error")
	notFoundErr := NewGroupNotFoundError("id", "")

	testCases := []struct {
		name          string
		groupID       string
		group         *Group
		setupMocks    func(mockStorage *MockDatabaseInterface)
		expectedGroup *Group
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
				mockStorage.EXPECT().UpdateGroup(gomock.Any(), "not-found", groupToUpdate).Return(nil, notFoundErr)
			},
			expectedGroup: nil,
			expectedErr:   notFoundErr,
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
			tc.setupMocks(mockStorage)

			group, err := s.UpdateGroup(context.Background(), tc.groupID, tc.group)

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, group)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedGroup, group)
			}
		})
	}
}

func TestService_DeleteGroup(t *testing.T) {
	groupID := "test-id"
	dbErr := errors.New("db error")
	authzErr := errors.New("authz error")
	notFoundErr := NewGroupNotFoundError("id", "")

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
			name:    "not found",
			groupID: "not-found",
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface) {
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), "not-found").Return(notFoundErr)
			},
			expectedErr: fmt.Errorf("failed to delete group from db: %w", notFoundErr),
		},
		{
			name:    "db error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface) {
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(dbErr)
			},
			expectedErr: fmt.Errorf("failed to delete group from db: %w", dbErr),
		},
		{
			name:    "authz error",
			groupID: groupID,
			setupMocks: func(mockStorage *MockDatabaseInterface, mockAuthz *MockAuthorizerInterface) {
				mockStorage.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(nil)
				mockAuthz.EXPECT().DeleteGroup(gomock.Any(), groupID).Return(authzErr)
			},
			expectedErr: fmt.Errorf("failed to delete group from authz: %w", authzErr),
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
			tc.setupMocks(mockStorage, mockAuthz)

			err := s.DeleteGroup(context.Background(), tc.groupID)

			if tc.expectedErr != nil {
				assert.EqualError(t, err, tc.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestService_AddUsersToGroup(t *testing.T) {
	groupID := "group-id"
	userIDs := []string{"user1", "user2"}
	dbErr := errors.New("db error")
	notFoundErr := NewGroupNotFoundError("id", "")

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
			name: "not found",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(notFoundErr)
			},
			expectedErr: fmt.Errorf("failed to add users to group: %w", notFoundErr),
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().AddUsersToGroup(gomock.Any(), groupID, userIDs).Return(dbErr)
			},
			expectedErr: fmt.Errorf("failed to add users to group: %w", dbErr),
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
			tc.setupMocks(mockStorage)

			err := s.AddUsersToGroup(context.Background(), groupID, userIDs)

			if tc.expectedErr != nil {
				assert.EqualError(t, err, tc.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestService_ListUsersInGroup(t *testing.T) {
	groupID := "group-id"
	expectedUsers := []string{"user1", "user2"}
	dbErr := errors.New("db error")
	notFoundErr := NewGroupNotFoundError("id", "")

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
				mockStorage.EXPECT().ListUsersInGroup(gomock.Any(), groupID).Return(nil, notFoundErr)
			},
			expectedUsers: nil,
			expectedErr:   notFoundErr,
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
			tc.setupMocks(mockStorage)

			users, err := s.ListUsersInGroup(context.Background(), groupID)

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, users)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedUsers, users)
			}
		})
	}
}

func TestService_RemoveUsersFromGroup(t *testing.T) {
	groupID := "group-id"
	userIDs := []string{"user1", "user2"}
	dbErr := errors.New("db error")
	notFoundErr := NewGroupNotFoundError("id", "")

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
				mockStorage.EXPECT().RemoveUsersFromGroup(gomock.Any(), groupID, userIDs).Return(notFoundErr)
			},
			expectedErr: notFoundErr,
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
			tc.setupMocks(mockStorage)

			err := s.RemoveUsersFromGroup(context.Background(), groupID, userIDs)

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestService_RemoveAllUsersFromGroup(t *testing.T) {
	groupID := "group-id"
	dbErr := errors.New("db error")
	notFoundErr := NewGroupNotFoundError("id", "")

	testCases := []struct {
		name        string
		setupMocks  func(mockStorage *MockDatabaseInterface)
		expectedErr error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveAllUsersFromGroup(gomock.Any(), groupID).Return([]string{"user1", "user2"}, nil)
			},
			expectedErr: nil,
		},
		{
			name: "not found",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveAllUsersFromGroup(gomock.Any(), groupID).Return(nil, notFoundErr)
			},
			expectedErr: fmt.Errorf("failed to remove users from group: %w", notFoundErr),
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveAllUsersFromGroup(gomock.Any(), groupID).Return(nil, dbErr)
			},
			expectedErr: fmt.Errorf("failed to remove users from group: %w", dbErr),
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
			tc.setupMocks(mockStorage)

			err := s.RemoveAllUsersFromGroup(context.Background(), groupID)

			if tc.expectedErr != nil {
				assert.EqualError(t, err, tc.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestService_GetGroupsForUser(t *testing.T) {
	userID := "user-id"
	expectedGroups := []*Group{{ID: "group1"}, {ID: "group2"}}
	dbErr := errors.New("db error")

	testCases := []struct {
		name           string
		setupMocks     func(mockStorage *MockDatabaseInterface)
		expectedGroups []*Group
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
				mockStorage.EXPECT().GetGroupsForUser(gomock.Any(), userID).Return([]*Group{}, nil)
			},
			expectedGroups: []*Group{},
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
			tc.setupMocks(mockStorage)

			groups, err := s.GetGroupsForUser(context.Background(), userID)

			if tc.expectedErr != nil {
				assert.ErrorIs(t, err, tc.expectedErr)
				assert.Nil(t, groups)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedGroups, groups)
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
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().UpdateGroupsForUser(gomock.Any(), userID, groupIDs).Return(dbErr)
			},
			expectedErr: fmt.Errorf("failed to get groups for user: %w", dbErr),
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
			tc.setupMocks(mockStorage)

			err := s.UpdateGroupsForUser(context.Background(), userID, groupIDs)

			if tc.expectedErr != nil {
				assert.EqualError(t, err, tc.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestService_RemoveGroupsForUser(t *testing.T) {
	userID := "user-id"
	dbErr := errors.New("db error")

	testCases := []struct {
		name        string
		setupMocks  func(mockStorage *MockDatabaseInterface)
		expectedErr error
	}{
		{
			name: "success",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveGroupsForUser(gomock.Any(), userID).Return([]string{"group1", "group2"}, nil)
			},
			expectedErr: nil,
		},
		{
			name: "db error",
			setupMocks: func(mockStorage *MockDatabaseInterface) {
				mockStorage.EXPECT().RemoveGroupsForUser(gomock.Any(), userID).Return(nil, dbErr)
			},
			expectedErr: fmt.Errorf("failed to get groups to remove for user: %w", dbErr),
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
			tc.setupMocks(mockStorage)

			err := s.RemoveGroupsForUser(context.Background(), userID)

			if tc.expectedErr != nil {
				assert.EqualError(t, err, tc.expectedErr.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
