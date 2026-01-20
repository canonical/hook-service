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

const (
	salesforceImportQuery       = "SELECT fHCM2__Email__c, Department2__c, fHCM2__Team__c FROM fHCM2__Team_Member__c WHERE fHCM2__Email__c != null"
	salesforceImportDescription = "Imported from Salesforce"
)

// SalesforceImporter handles importing users and groups from Salesforce
type SalesforceImporter struct {
	storage DatabaseInterface
	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// NewSalesforceImporter creates a new SalesforceImporter
func NewSalesforceImporter(
	storage DatabaseInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *SalesforceImporter {
	return &SalesforceImporter{
		storage: storage,
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}

// ImportUserGroups imports users and groups from Salesforce into the database
func (si *SalesforceImporter) ImportUserGroups(ctx context.Context, sfClient SalesforceClientInterface) (int, error) {
	ctx, span := si.tracer.Start(ctx, "groups.SalesforceImporter.ImportUserGroups")
	defer span.End()

	// Query all team members from Salesforce
	var members []SalesforceTeamMember
	
	if err := sfClient.Query(salesforceImportQuery, &members); err != nil {
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
					TenantId:    DefaultTenantID,
					Description: salesforceImportDescription,
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
					TenantId:    DefaultTenantID,
					Description: salesforceImportDescription,
					Type:        types.GroupTypeExternal,
				}
			}
			userGroupsMap[member.Email] = append(userGroupsMap[member.Email], member.Team)
		}
	}

	// Create all groups (ignore duplicates)
	createdGroupsMap := make(map[string]string) // group_name -> group_id
	for groupName, group := range groupsMap {
		createdGroup, err := si.storage.CreateGroup(ctx, group)
		if err != nil {
			if errors.Is(err, storage.ErrDuplicateKey) {
				// Group already exists - we'll handle this by fetching existing group memberships
				// and merging with new data during UpdateGroupsForUser
				si.logger.Infof("Group %s already exists, will merge memberships", groupName)
				continue
			}
			return 0, fmt.Errorf("failed to create group %s: %v", groupName, err)
		}
		createdGroupsMap[groupName] = createdGroup.ID
	}

	// For existing groups, we need to fetch them to get their IDs
	if len(createdGroupsMap) < len(groupsMap) {
		allGroups, err := si.storage.ListGroups(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to fetch existing groups: %v", err)
		}
		for _, g := range allGroups {
			if _, exists := groupsMap[g.Name]; exists {
				createdGroupsMap[g.Name] = g.ID
			}
		}
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
			if err := si.storage.UpdateGroupsForUser(ctx, userID, groupIDs); err != nil {
				si.logger.Warnf("Failed to update groups for user %s: %v", userID, err)
				continue
			}
			processedUsers++
		}
	}

	return processedUsers, nil
}
