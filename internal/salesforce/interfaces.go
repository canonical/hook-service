// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package salesforce

type SalesforceInterface interface {
	Query(string, any) error
}

// TeamMember represents a Salesforce team member record
type TeamMember struct {
	Email      string `mapstructure:"fHCM2__Email__c"`
	Department string `mapstructure:"Department2__c"`
	Team       string `mapstructure:"fHCM2__Team__c"`
}
