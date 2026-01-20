// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type ImportHandler struct {
	svc      ServiceInterface
	sfClient SalesforceClientInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

type ImportResponse struct {
	ProcessedUsers int    `json:"processed_users"`
	Message        string `json:"message"`
}

// HandleSalesforceImport handles the HTTP request to import users from Salesforce.
func (h *ImportHandler) HandleSalesforceImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, span := h.tracer.Start(ctx, "groups.ImportHandler.HandleSalesforceImport")
	defer span.End()

	if h.sfClient == nil {
		h.logger.Error("Salesforce client not configured")
		http.Error(w, "Salesforce integration not configured", http.StatusServiceUnavailable)
		return
	}

	processedUsers, err := h.svc.ImportUserGroupsFromSalesforce(ctx, h.sfClient)
	if err != nil {
		h.logger.Errorf("Failed to import from Salesforce: %v", err)
		http.Error(w, "Failed to import users from Salesforce", http.StatusInternalServerError)
		return
	}

	response := ImportResponse{
		ProcessedUsers: processedUsers,
		Message:        "Successfully imported users from Salesforce",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Errorf("Failed to encode response: %v", err)
	}
}

// NewImportHandler creates a new ImportHandler.
func NewImportHandler(
	svc ServiceInterface,
	sfClient SalesforceClientInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *ImportHandler {
	return &ImportHandler{
		svc:      svc,
		sfClient: sfClient,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
