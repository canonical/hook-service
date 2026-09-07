// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

// GrpcInterceptor provides gRPC authentication interceptors using a TokenVerifierInterface.
type GrpcInterceptor struct {
	verifier TokenVerifierInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// StreamAuthenticate returns a gRPC StreamServerInterceptor that validates incoming Bearer JWT tokens.
func (i *GrpcInterceptor) StreamAuthenticate() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, span := i.tracer.Start(ss.Context(), "authentication.GrpcInterceptor.StreamAuthenticate")
		defer span.End()

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Errorf(codes.Unauthenticated, "missing metadata")
		}

		values := md.Get("authorization")
		if len(values) == 0 {
			return status.Errorf(codes.Unauthenticated, "missing authorization header")
		}

		bearer := values[0]
		if !strings.HasPrefix(bearer, "Bearer ") {
			return status.Errorf(codes.Unauthenticated, "invalid authorization format")
		}

		token := strings.TrimPrefix(bearer, "Bearer ")

		claims, err := i.verifier.VerifyToken(ctx, token)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				i.logger.Debugf("gRPC JWT verification failed: %v", err)
				return status.Errorf(codes.Unauthenticated, "invalid token")
			}
			return status.Errorf(codes.Unauthenticated, "unauthorized")
		}

		if claims != nil && claims.Subject != "" {
			ctx = ContextWithUserID(ctx, claims.Subject)
		}

		return handler(srv, &contextedServerStream{ServerStream: ss, ctx: ctx})
	}
}

type contextedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextedServerStream) Context() context.Context {
	return s.ctx
}

// NewGrpcInterceptor creates a new GrpcInterceptor instance.
func NewGrpcInterceptor(verifier TokenVerifierInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *GrpcInterceptor {
	return &GrpcInterceptor{
		verifier: verifier,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
