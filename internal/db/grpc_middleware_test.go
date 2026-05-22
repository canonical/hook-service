// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"testing"

	"google.golang.org/grpc"
)

func TestIsReadOnlyMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{"/hook.groups.v1.GroupsMappingService/GetGroupsForUser", true},
		{"/hook.groups.v1.GroupsMappingService/GetUsersInGroup", true},
		{"/hook.groups.v1.GroupsMappingService/ListUsers", true},
		{"/hook.groups.v1.GroupsMappingService/QueryGroups", true},
		{"/hook.groups.v1.GroupsMappingService/DescribeGroup", true},
		{"/hook.groups.v1.GroupsMappingService/SearchGroups", true},
		{"/hook.groups.v1.GroupsMappingService/CreateGroup", false},
		{"/hook.groups.v1.GroupsMappingService/DeleteGroup", false},
		{"/hook.groups.v1.GroupsMappingService/UpdateGroup", false},
		{"invalidMethodName", false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := isReadOnlyMethod(tt.method)
			if got != tt.want {
				t.Errorf("isReadOnlyMethod(%q) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}

func TestUnaryReplicaRoutingInterceptor(t *testing.T) {
	interceptor := UnaryReplicaRoutingInterceptor()

	tests := []struct {
		name         string
		method       string
		setupContext func() context.Context
		wantReadOnly bool
	}{
		{
			name:   "read-only method without transaction",
			method: "/hook.groups.v1.GroupsMappingService/GetGroupsForUser",
			setupContext: func() context.Context {
				return context.Background()
			},
			wantReadOnly: true,
		},
		{
			name:   "read-only method with active transaction",
			method: "/hook.groups.v1.GroupsMappingService/GetGroupsForUser",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), txContextKey, &mockTx{})
			},
			wantReadOnly: false,
		},
		{
			name:   "read-only method with lazy transaction",
			method: "/hook.groups.v1.GroupsMappingService/GetGroupsForUser",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), lazyTxContextKey, &lazyTx{})
			},
			wantReadOnly: false,
		},
		{
			name:   "mutating method without transaction",
			method: "/hook.groups.v1.GroupsMappingService/CreateGroup",
			setupContext: func() context.Context {
				return context.Background()
			},
			wantReadOnly: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupContext()
			info := &grpc.UnaryServerInfo{FullMethod: tt.method}

			var handlerCalled bool
			handler := func(handlerCtx context.Context, req interface{}) (interface{}, error) {
				handlerCalled = true
				isRO := readOnlyFromContext(handlerCtx)
				if isRO != tt.wantReadOnly {
					t.Errorf("readOnlyFromContext = %v, want %v", isRO, tt.wantReadOnly)
				}
				return nil, nil
			}

			_, err := interceptor(ctx, nil, info, handler)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !handlerCalled {
				t.Error("expected handler to be called")
			}
		})
	}
}

type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestStreamReplicaRoutingInterceptor(t *testing.T) {
	interceptor := StreamReplicaRoutingInterceptor()

	tests := []struct {
		name         string
		method       string
		setupContext func() context.Context
		wantReadOnly bool
	}{
		{
			name:   "read-only method without transaction",
			method: "/hook.groups.v1.GroupsMappingService/GetGroupsForUser",
			setupContext: func() context.Context {
				return context.Background()
			},
			wantReadOnly: true,
		},
		{
			name:   "read-only method with active transaction",
			method: "/hook.groups.v1.GroupsMappingService/GetGroupsForUser",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), txContextKey, &mockTx{})
			},
			wantReadOnly: false,
		},
		{
			name:   "read-only method with lazy transaction",
			method: "/hook.groups.v1.GroupsMappingService/GetGroupsForUser",
			setupContext: func() context.Context {
				return context.WithValue(context.Background(), lazyTxContextKey, &lazyTx{})
			},
			wantReadOnly: false,
		},
		{
			name:   "mutating method without transaction",
			method: "/hook.groups.v1.GroupsMappingService/CreateGroup",
			setupContext: func() context.Context {
				return context.Background()
			},
			wantReadOnly: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := &mockServerStream{ctx: tt.setupContext()}
			info := &grpc.StreamServerInfo{FullMethod: tt.method}

			var handlerCalled bool
			handler := func(srv interface{}, stream grpc.ServerStream) error {
				handlerCalled = true
				isRO := readOnlyFromContext(stream.Context())
				if isRO != tt.wantReadOnly {
					t.Errorf("readOnlyFromContext = %v, want %v", isRO, tt.wantReadOnly)
				}
				return nil
			}

			err := interceptor(nil, ss, info, handler)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !handlerCalled {
				t.Error("expected handler to be called")
			}
		})
	}
}
