// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"strings"

	"google.golang.org/grpc"
)

// UnaryReplicaRoutingInterceptor routes read-only unary RPCs to the read-only replica database pool,
// provided that no active transaction exists in the context.
func UnaryReplicaRoutingInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if isReadOnlyMethod(info.FullMethod) && !hasTransaction(ctx) {
			ctx = WithReadOnly(ctx)
		}
		return handler(ctx, req)
	}
}

// StreamReplicaRoutingInterceptor routes read-only streaming RPCs to the read-only replica database pool,
// provided that no active transaction exists in the context.
func StreamReplicaRoutingInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isReadOnlyMethod(info.FullMethod) && !hasTransaction(ss.Context()) {
			ctx := WithReadOnly(ss.Context())
			return handler(srv, &contextedServerStream{ServerStream: ss, ctx: ctx})
		}
		return handler(srv, ss)
	}
}

// contextedServerStream wraps grpc.ServerStream to override the context.
type contextedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the wrapped context.
func (s *contextedServerStream) Context() context.Context {
	return s.ctx
}

func hasTransaction(ctx context.Context) bool {
	return ctx.Value(txContextKey) != nil || ctx.Value(lazyTxContextKey) != nil
}

func isReadOnlyMethod(fullMethod string) bool {
	parts := strings.Split(fullMethod, "/")
	if len(parts) == 0 {
		return false
	}
	method := parts[len(parts)-1]
	return strings.HasPrefix(method, "Get") ||
		strings.HasPrefix(method, "List") ||
		strings.HasPrefix(method, "Query") ||
		strings.HasPrefix(method, "Describe") ||
		strings.HasPrefix(method, "Search")
}
