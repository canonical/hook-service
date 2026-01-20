// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
)

var _ ServiceInterface = (*Service)(nil)

type Service struct {
	db    DatabaseInterface
	authz AuthorizerInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) ListGroups(ctx context.Context) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListGroups")
	defer span.End()

	return s.db.ListGroups(ctx)
}

func (s *Service) CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.CreateGroup")
	defer span.End()

	if group.ID != "" {
		return nil, ErrInvalidGroupID
	}

	createdGroup, err := s.db.CreateGroup(ctx, group)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateKey) {
			return nil, ErrDuplicateGroup
		}
		return nil, err
	}
	return createdGroup, nil
}

func (s *Service) GetGroup(ctx context.Context, id string) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.GetGroup")
	defer span.End()

	group, err := s.db.GetGroup(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return group, nil
}

func (s *Service) UpdateGroup(ctx context.Context, id string, group *types.Group) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.UpdateGroup")
	defer span.End()

	updated, err := s.db.UpdateGroup(ctx, id, group)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.DeleteGroup")
	defer span.End()

	if err := s.db.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from db: %v", err)
	}
	if err := s.authz.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from authz: %v", err)
	}
	return nil
}

func (s *Service) AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.AddUsersToGroup")
	defer span.End()

	if len(userIDs) == 0 {
		return nil
	}

	if err := s.db.AddUsersToGroup(ctx, groupID, userIDs); err != nil {
		if errors.Is(err, storage.ErrDuplicateKey) {
			return ErrUserAlreadyInGroup
		}
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			return ErrInvalidGroupID
		}
		return fmt.Errorf("failed to add users to group: %v", err)
	}
	return nil
}

func (s *Service) ListUsersInGroup(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListUsersInGroup")
	defer span.End()

	g, err := s.db.ListUsersInGroup(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to list users in group: %w", err)
	}
	return g, nil
}

func (s *Service) RemoveUsersFromGroup(ctx context.Context, groupID string, users []string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.RemoveUsersFromGroup")
	defer span.End()

	if err := s.db.RemoveUsersFromGroup(ctx, groupID, users); err != nil {
		return fmt.Errorf("failed to remove users from group: %w", err)
	}
	return nil
}

func (s *Service) GetGroupsForUser(ctx context.Context, userID string) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.GetGroupsForUser")
	defer span.End()

	groups, err := s.db.GetGroupsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get groups for user: %w", err)
	}
	return groups, nil
}

func (s *Service) UpdateGroupsForUser(ctx context.Context, userID string, groupIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.UpdateGroupsForUser")
	defer span.End()

	if err := s.db.UpdateGroupsForUser(ctx, userID, groupIDs); err != nil {
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			return ErrInvalidGroupID
		}
		return err
	}
	return nil
}

func (s *Service) ImportUserGroupsFromSalesforce(ctx context.Context, sfClient SalesforceClientInterface) (int, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ImportUserGroupsFromSalesforce")
	defer span.End()

	// Query all team members from Salesforce
	query := "SELECT fHCM2__Email__c, Department2__c, fHCM2__Team__c FROM fHCM2__Team_Member__c WHERE fHCM2__Email__c != null"
	var members []SalesforceTeamMember
	
	if err := sfClient.Query(query, &members); err != nil {
		return 0, fmt.Errorf("failed to query salesforce: %v", err)
	}

	// Track groups and user memberships
	groupsMap := make(map[string]*types.Group)
	userGroupsMap := make(map[string][]string) // user_id -> group_ids
	
	for _, member := range members {
		if member.Email == "" {
			continue
		}
		
		// Add department group if present
		if member.Department != "" {
			if _, exists := groupsMap[member.Department]; !exists {
				groupsMap[member.Department] = &types.Group{
					Name:        member.Department,
					TenantId:    "default",
					Description: "Imported from Salesforce",
					Type:        types.GroupTypeExternal,
				}
			}
			userGroupsMap[member.Email] = append(userGroupsMap[member.Email], member.Department)
		}
		
		// Add team group if present
		if member.Team != "" {
			if _, exists := groupsMap[member.Team]; !exists {
				groupsMap[member.Team] = &types.Group{
					Name:        member.Team,
					TenantId:    "default",
					Description: "Imported from Salesforce",
					Type:        types.GroupTypeExternal,
				}
			}
			userGroupsMap[member.Email] = append(userGroupsMap[member.Email], member.Team)
		}
	}

	// Create all groups (ignore duplicates)
	createdGroupsMap := make(map[string]string) // group_name -> group_id
	for _, group := range groupsMap {
		createdGroup, err := s.db.CreateGroup(ctx, group)
		if err != nil {
			if errors.Is(err, storage.ErrDuplicateKey) {
				// Group already exists, fetch it to get the ID
				existingGroup, fetchErr := s.db.ListGroups(ctx)
				if fetchErr == nil {
					for _, g := range existingGroup {
						if g.Name == group.Name {
							createdGroupsMap[group.Name] = g.ID
							break
						}
					}
				}
				continue
			}
			return 0, fmt.Errorf("failed to create group %s: %v", group.Name, err)
		}
		createdGroupsMap[group.Name] = createdGroup.ID
	}

	// Add users to groups
	processedUsers := 0
	for userID, groupNames := range userGroupsMap {
		groupIDs := make([]string, 0, len(groupNames))
		for _, groupName := range groupNames {
			if groupID, exists := createdGroupsMap[groupName]; exists {
				groupIDs = append(groupIDs, groupID)
			}
		}
		
		if len(groupIDs) > 0 {
			if err := s.db.UpdateGroupsForUser(ctx, userID, groupIDs); err != nil {
				s.logger.Warnf("Failed to update groups for user %s: %v", userID, err)
				continue
			}
			processedUsers++
		}
	}

	return processedUsers, nil
}

func NewService(
	db DatabaseInterface,
	authz AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.db = db
	s.authz = authz

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}
