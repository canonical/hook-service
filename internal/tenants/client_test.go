// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_logger.go -source=../../internal/logging/interfaces.go

func TestClientValidateMembership(t *testing.T) {
	tests := []struct {
		name       string
		identityID string
		tenantID   string
		handler    http.HandlerFunc
		expectErr  error
	}{
		{
			name:       "membership valid",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("identity_id") != "user-123" {
					t.Errorf("expected identity_id=user-123, got %s", r.URL.Query().Get("identity_id"))
				}
				json.NewEncoder(w).Encode(lookupResponse{
					Tenants: []tenant{
						{ID: "tenant-abc"},
						{ID: "tenant-def"},
					},
				})
			},
			expectErr: nil,
		},
		{
			name:       "membership denied — tenant not in list",
			identityID: "user-123",
			tenantID:   "tenant-xyz",
			handler: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(lookupResponse{
					Tenants: []tenant{
						{ID: "tenant-abc"},
					},
				})
			},
			expectErr: ErrNotMember,
		},
		{
			name:       "membership denied — empty tenant list",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			handler: func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(lookupResponse{Tenants: []tenant{}})
			},
			expectErr: ErrNotMember,
		},
		{
			name:       "tenant-service returns 500",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectErr: errors.New("tenant-service returned status 500"),
		},
		{
			name:       "tenant-service returns 404",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			expectErr: errors.New("tenant-service returned status 404"),
		},
		{
			name:       "tenant-service returns invalid JSON",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("not json"))
			},
			expectErr: errors.New("cannot decode tenant-service response"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()

			// Use a simple noop tracer for unit tests.
			tracer := &noopTracer{}

			client := NewClient(server.URL, tracer, nil, nil)
			err := client.ValidateMembership(context.Background(), test.identityID, test.tenantID)

			if test.expectErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error %v, got nil", test.expectErr)
			}

			// For sentinel errors, check with errors.Is.
			if errors.Is(test.expectErr, ErrNotMember) {
				if !errors.Is(err, ErrNotMember) {
					t.Fatalf("expected ErrNotMember, got %v", err)
				}
				return
			}

			// For non-sentinel errors, check the message contains expected text.
			if err.Error() != test.expectErr.Error() && !containsSubstring(err.Error(), test.expectErr.Error()) {
				t.Fatalf("expected error containing %q, got %q", test.expectErr.Error(), err.Error())
			}
		})
	}
}

func TestClientValidateMembershipUnreachable(t *testing.T) {
	tracer := &noopTracer{}
	client := NewClient("http://127.0.0.1:1", tracer, nil, nil)
	err := client.ValidateMembership(context.Background(), "user-123", "tenant-abc")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

// noopTracer satisfies TracingInterface without requiring gomock.
type noopTracer struct{}

func (n *noopTracer) Start(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
