// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package importer

import (
	"context"
	"fmt"

	"github.com/canonical/hook-service/internal/salesforce"
)

const allTeamMembersQuery = "SELECT fHCM2__Email__c, Department2__c, fHCM2__Team__c FROM fHCM2__Team_Member__c"

// TeamMemberRecord represents a single Salesforce team member record.
type TeamMemberRecord struct {
	Email      string `mapstructure:"fHCM2__Email__c"`
	Department string `mapstructure:"Department2__c"`
	Team       string `mapstructure:"fHCM2__Team__c"`
}

// SalesforceDriver implements DriverInterface by querying the Salesforce API
// for all team member records and extracting user-to-group mappings.
type SalesforceDriver struct {
	client salesforce.SalesforceInterface
}

// NewSalesforceDriver creates a new SalesforceDriver with the given Salesforce client.
func NewSalesforceDriver(client salesforce.SalesforceInterface) *SalesforceDriver {
	return &SalesforceDriver{client: client}
}

func (d *SalesforceDriver) Prefix() string {
	return "salesforce"
}

// FetchAllUserGroups queries Salesforce for all team members and returns
// a flat list of user-to-group mappings. Each user may have up to two
// group mappings: one for their department and one for their team.
func (d *SalesforceDriver) FetchAllUserGroups(ctx context.Context) ([]UserGroupMapping, error) {
	var records []TeamMemberRecord
	if err := d.client.Query(allTeamMembersQuery, &records); err != nil {
		return nil, fmt.Errorf("failed to query salesforce team members: %w", err)
	}

	mappings := make([]UserGroupMapping, 0, len(records)*2)
	for _, r := range records {
		if r.Email == "" {
			continue
		}
		if r.Department != "" {
			mappings = append(mappings, UserGroupMapping{
				UserID:    r.Email,
				GroupName: r.Department,
			})
		}
		if r.Team != "" {
			mappings = append(mappings, UserGroupMapping{
				UserID:    r.Email,
				GroupName: r.Team,
			})
		}
	}

	return mappings, nil
}
