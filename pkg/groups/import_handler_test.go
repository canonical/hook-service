// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func TestImportHandler_HandleSalesforceImport(t *testing.T) {
	tests := []struct {
		name               string
		setupMocks         func(*gomock.Controller) (ServiceInterface, SalesforceClientInterface)
		expectedStatusCode int
		expectedBody       *ImportResponse
	}{
		{
			name: "Success",
			setupMocks: func(ctrl *gomock.Controller) (ServiceInterface, SalesforceClientInterface) {
				svc := NewMockServiceInterface(ctrl)
				sfClient := NewMockSalesforceClientInterface(ctrl)
				
				svc.EXPECT().ImportUserGroupsFromSalesforce(gomock.Any(), sfClient).Return(5, nil)
				
				return svc, sfClient
			},
			expectedStatusCode: http.StatusOK,
			expectedBody: &ImportResponse{
				ProcessedUsers: 5,
				Message:        "Successfully imported users from Salesforce",
			},
		},
		{
			name: "Service returns error",
			setupMocks: func(ctrl *gomock.Controller) (ServiceInterface, SalesforceClientInterface) {
				svc := NewMockServiceInterface(ctrl)
				sfClient := NewMockSalesforceClientInterface(ctrl)
				
				svc.EXPECT().ImportUserGroupsFromSalesforce(gomock.Any(), sfClient).Return(0, errors.New("import failed"))
				
				return svc, sfClient
			},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       nil,
		},
		{
			name: "Salesforce client not configured",
			setupMocks: func(ctrl *gomock.Controller) (ServiceInterface, SalesforceClientInterface) {
				svc := NewMockServiceInterface(ctrl)
				return svc, nil
			},
			expectedStatusCode: http.StatusServiceUnavailable,
			expectedBody:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "groups.ImportHandler.HandleSalesforceImport").Return(context.Background(), trace.SpanFromContext(context.Background()))

			svc, sfClient := tt.setupMocks(ctrl)

			if sfClient == nil {
				mockLogger.EXPECT().Error("Salesforce client not configured")
			}
			if tt.expectedStatusCode == http.StatusInternalServerError {
				mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any())
			}

			handler := NewImportHandler(svc, sfClient, mockTracer, mockMonitor, mockLogger)

			req := httptest.NewRequest(http.MethodPost, "/api/v0/authz/groups/import/salesforce", nil)
			w := httptest.NewRecorder()

			handler.HandleSalesforceImport(w, req)

			if w.Code != tt.expectedStatusCode {
				t.Errorf("expected status code %d, got %d", tt.expectedStatusCode, w.Code)
			}

			if tt.expectedBody != nil {
				var response ImportResponse
				if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}

				if response.ProcessedUsers != tt.expectedBody.ProcessedUsers {
					t.Errorf("expected processed users %d, got %d", tt.expectedBody.ProcessedUsers, response.ProcessedUsers)
				}
				if response.Message != tt.expectedBody.Message {
					t.Errorf("expected message %q, got %q", tt.expectedBody.Message, response.Message)
				}
			}
		})
	}
}
