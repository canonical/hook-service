// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package importer

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_salesforce.go -source=../../internal/salesforce/interfaces.go

func TestSalesforceDriverFetchAllUserGroups(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*MockSalesforceInterface)
		expectedLen int
		expectErr   bool
	}{
		{
			name: "multiple records with department and team",
			setupMock: func(m *MockSalesforceInterface) {
				m.EXPECT().Query(allTeamMembersQuery, gomock.Any()).DoAndReturn(
					func(_ string, result any) error {
						ptr := result.(*[]TeamMemberRecord)
						*ptr = []TeamMemberRecord{
							{Email: "alice@example.com", Department: "Engineering", Team: "Platform"},
							{Email: "bob@example.com", Department: "Sales", Team: "EMEA"},
						}
						return nil
					},
				)
			},
			expectedLen: 4,
		},
		{
			name: "record with empty email is skipped",
			setupMock: func(m *MockSalesforceInterface) {
				m.EXPECT().Query(allTeamMembersQuery, gomock.Any()).DoAndReturn(
					func(_ string, result any) error {
						ptr := result.(*[]TeamMemberRecord)
						*ptr = []TeamMemberRecord{
							{Email: "", Department: "Engineering", Team: "Platform"},
							{Email: "bob@example.com", Department: "Sales", Team: ""},
						}
						return nil
					},
				)
			},
			expectedLen: 1, // only bob's Sales department
		},
		{
			name: "record with empty department and team",
			setupMock: func(m *MockSalesforceInterface) {
				m.EXPECT().Query(allTeamMembersQuery, gomock.Any()).DoAndReturn(
					func(_ string, result any) error {
						ptr := result.(*[]TeamMemberRecord)
						*ptr = []TeamMemberRecord{
							{Email: "alice@example.com", Department: "", Team: ""},
						}
						return nil
					},
				)
			},
			expectedLen: 0,
		},
		{
			name: "empty records",
			setupMock: func(m *MockSalesforceInterface) {
				m.EXPECT().Query(allTeamMembersQuery, gomock.Any()).DoAndReturn(
					func(_ string, result any) error {
						ptr := result.(*[]TeamMemberRecord)
						*ptr = []TeamMemberRecord{}
						return nil
					},
				)
			},
			expectedLen: 0,
		},
		{
			name: "query error",
			setupMock: func(m *MockSalesforceInterface) {
				m.EXPECT().Query(allTeamMembersQuery, gomock.Any()).Return(errors.New("connection refused"))
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := NewMockSalesforceInterface(ctrl)
			tt.setupMock(mockClient)

			driver := NewSalesforceDriver(mockClient)
			mappings, err := driver.FetchAllUserGroups(context.Background())

			if tt.expectErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.expectErr && len(mappings) != tt.expectedLen {
				t.Fatalf("expected %d mappings, got %d", tt.expectedLen, len(mappings))
			}
		})
	}
}
