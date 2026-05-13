package groups

import (
	"errors"

	pb "github.com/canonical/hook-service/gen/hook/groups/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
)

var _ pb.GroupsMappingServiceServer = (*MappingGrpcServer)(nil)

type MappingGrpcServer struct {
	svc ServiceInterface
	pb.UnimplementedGroupsMappingServiceServer

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (m *MappingGrpcServer) GetGroupsForUser(req *pb.GetGroupsForUserReq, stream grpc.ServerStreamingServer[pb.GroupMapping]) error {
	ctx, span := m.tracer.Start(stream.Context(), "groups.MappingGrpcServer.GetGroupsForUser")
	defer span.End()

	span.SetAttributes(
		attribute.String("user.id", req.GetUserId()),
		attribute.String("tenant.id", req.GetTenantId()),
	)

	if req.GetUserId() == "" {
		err := ErrInvalidUserID
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "user id is empty")
		return mapMappingErrorToStatus(err, "stream groups for user")
	}

	err := m.svc.StreamGroupsForUser(ctx, req.GetTenantId(), req.GetUserId(), func(g *types.Group) error {
		return stream.Send(&pb.GroupMapping{
			Id:          g.ID,
			Name:        g.Name,
			TenantId:    g.TenantId,
			Description: g.Description,
			Type:        g.Type.String(),
			CreatedAt:   timestamppb.New(g.CreatedAt),
			UpdatedAt:   timestamppb.New(g.UpdatedAt),
		})
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "stream groups for user failed")
		return mapMappingErrorToStatus(err, "stream groups for user")
	}

	span.SetStatus(otelcodes.Ok, "groups streamed successfully")
	return nil
}

func (m *MappingGrpcServer) GetUsersInGroup(req *pb.GetUsersInGroupReq, stream grpc.ServerStreamingServer[pb.UserMapping]) error {
	ctx, span := m.tracer.Start(stream.Context(), "groups.MappingGrpcServer.GetUsersInGroup")
	defer span.End()

	span.SetAttributes(
		attribute.String("group.id", req.GetGroupId()),
		attribute.String("tenant.id", req.GetTenantId()),
	)

	if req.GetGroupId() == "" {
		err := ErrInvalidGroupID
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "group id is empty")
		return mapMappingErrorToStatus(err, "stream users in group")
	}

	err := m.svc.StreamUsersInGroup(ctx, req.GetTenantId(), req.GetGroupId(), func(userID string) error {
		return stream.Send(&pb.UserMapping{Id: userID})
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "stream users in group failed")
		return mapMappingErrorToStatus(err, "stream users in group")
	}

	span.SetStatus(otelcodes.Ok, "users streamed successfully")
	return nil
}

func mapMappingErrorToStatus(err error, action string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidTenant):
		return status.Errorf(codes.InvalidArgument, "invalid tenant")
	case errors.Is(err, ErrInvalidGroupID):
		return status.Errorf(codes.InvalidArgument, "invalid group id")
	case errors.Is(err, ErrInvalidUserID):
		return status.Errorf(codes.InvalidArgument, "invalid user id")
	case errors.Is(err, ErrStreamInterrupted):
		return status.Errorf(codes.Internal, "stream interrupted: %v", err)
	case errors.Is(err, ErrUnauthorizedStream):
		return status.Errorf(codes.Unauthenticated, "unauthorized")
	case errors.Is(err, storage.ErrNotFound):
		return status.Errorf(codes.NotFound, "not found")
	default:
		return status.Errorf(codes.Internal, "%s failed", action)
	}
}

func NewMappingGrpcServer(svc ServiceInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *MappingGrpcServer {
	return &MappingGrpcServer{
		svc:     svc,
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}
