// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"context"
	"encoding/json"

	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/canonical/hook-service/internal/config"
)

//go:generate mockgen -build_flags=--mod=mod -package status -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package status -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package status -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func TestAliveOK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := NewMockLoggerInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockTracer := NewMockTracingInterface(ctrl)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/status", nil)
	w := httptest.NewRecorder()

	mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Times(1).Return(context.TODO(), trace.SpanFromContext(req.Context()))

	mux := chi.NewMux()
	NewAPI(config.ModeStandalone, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)

	mux.ServeHTTP(w, req)
	res := w.Result()
	defer res.Body.Close()
	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("expected error to be nil got %v", err)
	}
	receivedStatus := new(Status)
	if err := json.Unmarshal(data, receivedStatus); err != nil {
		t.Fatalf("expected error to be nil got %v", err)
	}
	if receivedStatus.Status != "ok" {
		t.Fatalf("expected status to be ok got %v", receivedStatus.Status)
	}
}

func TestAliveReportsDeploymentMode(t *testing.T) {
	tests := []struct {
		name string
		mode config.DeploymentMode
	}{
		{
			name: "platform mode",
			mode: config.ModePlatform,
		},
		{
			name: "standalone mode",
			mode: config.ModeStandalone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)

			req := httptest.NewRequest(http.MethodGet, "/api/v0/status", nil)
			w := httptest.NewRecorder()

			mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Times(1).
				Return(t.Context(), trace.SpanFromContext(req.Context()))

			mux := chi.NewMux()
			NewAPI(tt.mode, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)
			mux.ServeHTTP(w, req)

			res := w.Result()
			defer func() { _ = res.Body.Close() }()

			receivedStatus := new(Status)
			if err := json.NewDecoder(res.Body).Decode(receivedStatus); err != nil {
				t.Fatalf("expected error to be nil got %v", err)
			}
			if receivedStatus.Mode != tt.mode {
				t.Errorf("expected mode %q got %q", tt.mode, receivedStatus.Mode)
			}
		})
	}
}
