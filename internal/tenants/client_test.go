// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package tenants

import (
	"context"
	"errors"
	"testing"
	"time"

	tenantpb "github.com/canonical/identity-platform-api/v0/tenant"
	"go.uber.org/mock/gomock"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package tenants -destination ./mock_tenant_service_client.go -source=./interfaces.go

func TestClientValidateMembership(t *testing.T) {
	tests := []struct {
		name       string
		identityID string
		tenantID   string
		mockClient func(*MockTenantServiceClientInterface)
		expectErr  error
	}{
		{
			name:       "membership valid",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			mockClient: func(client *MockTenantServiceClientInterface) {
				client.EXPECT().LookupTenants(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *tenantpb.LookupTenantsRequest, _ ...grpc.CallOption) (*tenantpb.LookupTenantsResponse, error) {
					if req.GetIdentityId() != "user-123" {
						t.Fatalf("expected identity_id=user-123, got %s", req.GetIdentityId())
					}

					return &tenantpb.LookupTenantsResponse{
						Tenants: []*tenantpb.Tenant{{Id: "tenant-abc"}, {Id: "tenant-def"}},
					}, nil
				})
			},
			expectErr: nil,
		},
		{
			name:       "membership denied — tenant not in list",
			identityID: "user-123",
			tenantID:   "tenant-xyz",
			mockClient: func(client *MockTenantServiceClientInterface) {
				client.EXPECT().LookupTenants(gomock.Any(), gomock.Any()).Return(&tenantpb.LookupTenantsResponse{
					Tenants: []*tenantpb.Tenant{{Id: "tenant-abc"}},
				}, nil)
			},
			expectErr: ErrNotMember,
		},
		{
			name:       "grpc client returns error",
			identityID: "user-123",
			tenantID:   "tenant-abc",
			mockClient: func(client *MockTenantServiceClientInterface) {
				client.EXPECT().LookupTenants(gomock.Any(), gomock.Any()).Return(nil, errors.New("backend unavailable"))
			},
			expectErr: errors.New("cannot look up tenants: backend unavailable"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			grpcClient := NewMockTenantServiceClientInterface(ctrl)
			test.mockClient(grpcClient)

			tracer := &noopTracer{}
			client := NewClient(grpcClient, 5*time.Second, tracer, nil, nil)
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

			if err.Error() != test.expectErr.Error() {
				t.Fatalf("expected error %q, got %q", test.expectErr.Error(), err.Error())
			}
		})
	}
}

// noopTracer satisfies TracingInterface without requiring gomock.
type noopTracer struct{}

func (n *noopTracer) Start(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}
