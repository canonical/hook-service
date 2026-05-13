package authentication

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type GrpcInterceptor struct {
	verifier TokenVerifierInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

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

		authorized, err := i.verifier.VerifyToken(ctx, token)
		if err != nil {
			i.logger.Debugf("gRPC JWT verification failed: %v", err)
			return status.Errorf(codes.Unauthenticated, "invalid token")
		}

		if !authorized {
			return status.Errorf(codes.Unauthenticated, "unauthorized")
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

func NewGrpcInterceptor(verifier TokenVerifierInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *GrpcInterceptor {
	return &GrpcInterceptor{
		verifier: verifier,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
