// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"net/http"

	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"github.com/gogo/protobuf/proto"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
)

var _ v0_groups.AuthzGroupsServiceServer = (*GrpcServer)(nil)

const DefaultTenantID = "default"

type GrpcServer struct {
	svc ServiceInterface
	v0_groups.UnimplementedAuthzGroupsServiceServer

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (g *GrpcServer) CreateGroup(ctx context.Context, req *v0_groups.CreateGroupReq) (*v0_groups.CreateGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.CreateGroup")
	defer span.End()

	if req.Group == nil {
		err := status.Errorf(codes.InvalidArgument, "group cannot be nil")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	span.SetAttributes(
		attribute.String("group.name", req.Group.GetName()),
		attribute.String("group.type", req.Group.GetType()),
	)

	gType, err := types.ParseGroupType(req.Group.GetType())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid group type")
		return nil, g.mapErrorToStatus(err, "create group")
	}

	group := &types.Group{
		Name:        req.Group.GetName(),
		TenantId:    DefaultTenantID,
		Description: req.Group.GetDescription(),
		Type:        gType,
	}

	group, err = g.svc.CreateGroup(ctx, group)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "create group failed")
		return nil, g.mapErrorToStatus(err, "create group")
	}

	span.SetAttributes(attribute.String("group.id", group.ID))
	span.SetStatus(otelcodes.Ok, "group created successfully")

	return &v0_groups.CreateGroupResp{
		Data: []*v0_groups.Group{{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        group.Type.String(),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: proto.String("Group created"),
	}, nil
}

func (g *GrpcServer) GetGroup(ctx context.Context, req *v0_groups.GetGroupReq) (*v0_groups.GetGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.GetGroup")
	defer span.End()

	span.SetAttributes(attribute.String("group.id", req.Id))

	group, err := g.svc.GetGroup(ctx, req.Id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "get group failed")
		return nil, g.mapErrorToStatus(err, "get group")
	}

	span.SetAttributes(attribute.String("group.name", group.Name))
	span.SetStatus(otelcodes.Ok, "group retrieved successfully")

	return &v0_groups.GetGroupResp{
		Data: []*v0_groups.Group{{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        group.Type.String(),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: proto.String("Group details"),
	}, nil
}

func (g *GrpcServer) ListGroups(ctx context.Context, req *v0_groups.ListGroupsReq) (*v0_groups.ListGroupsResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.ListGroups")
	defer span.End()

	groups, err := g.svc.ListGroups(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "list groups failed")
		return nil, g.mapErrorToStatus(err, "list groups")
	}

	respGroups := make([]*v0_groups.Group, len(groups))
	for i, group := range groups {
		respGroups[i] = &v0_groups.Group{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        group.Type.String(),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}
	}

	span.SetAttributes(attribute.Int("groups.count", len(groups)))
	span.SetStatus(otelcodes.Ok, "groups listed successfully")

	return &v0_groups.ListGroupsResp{
		Data:    respGroups,
		Status:  http.StatusOK,
		Message: proto.String("Group list"),
	}, nil
}

func (g *GrpcServer) RemoveGroup(ctx context.Context, req *v0_groups.RemoveGroupReq) (*v0_groups.RemoveGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.RemoveGroup")
	defer span.End()

	span.SetAttributes(attribute.String("group.id", req.Id))

	err := g.svc.DeleteGroup(ctx, req.Id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "delete group failed")
		return nil, g.mapErrorToStatus(err, "delete group")
	}

	span.SetStatus(otelcodes.Ok, "group deleted successfully")

	return &v0_groups.RemoveGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("Group deleted"),
	}, nil
}

func (g *GrpcServer) UpdateGroup(ctx context.Context, req *v0_groups.UpdateGroupReq) (*v0_groups.UpdateGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.UpdateGroup")
	defer span.End()

	span.SetAttributes(attribute.String("group.id", req.GetId()))

	if req.Group == nil {
		err := status.Errorf(codes.InvalidArgument, "group cannot be nil")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	if req.Group.GetName() != "" {
		err := status.Errorf(codes.InvalidArgument, "group name cannot be updated")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	gType, err := types.ParseGroupType(req.Group.GetType())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid group type")
		return nil, g.mapErrorToStatus(err, "update group")
	}

	group := &types.Group{
		Description: req.Group.GetDescription(),
		Type:        gType,
		TenantId:    DefaultTenantID,
	}

	gg, err := g.svc.UpdateGroup(ctx, req.GetId(), group)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "update group failed")
		return nil, g.mapErrorToStatus(err, "update group")
	}

	span.SetStatus(otelcodes.Ok, "group updated successfully")

	return &v0_groups.UpdateGroupResp{
		Data: []*v0_groups.Group{{
			Id:          gg.ID,
			Name:        gg.Name,
			TenantId:    gg.TenantId,
			Description: gg.Description,
			Type:        gg.Type.String(),
			CreatedAt:   timestamppb.New(gg.CreatedAt),
			UpdatedAt:   timestamppb.New(gg.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: proto.String("Group updated"),
	}, nil
}

func (g *GrpcServer) ListUsersInGroup(ctx context.Context, req *v0_groups.ListUsersInGroupReq) (*v0_groups.ListUsersInGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.ListUsersInGroup")
	defer span.End()

	span.SetAttributes(attribute.String("group.id", req.GetId()))

	users, err := g.svc.ListUsersInGroup(ctx, req.GetId())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "list users in group failed")
		return nil, g.mapErrorToStatus(err, "list users in group")
	}

	respUsers := make([]*v0_groups.User, len(users))
	for i, user := range users {
		respUsers[i] = &v0_groups.User{Id: user}
	}

	span.SetAttributes(attribute.Int("users.count", len(users)))
	span.SetStatus(otelcodes.Ok, "users listed successfully")

	return &v0_groups.ListUsersInGroupResp{
		Data:    respUsers,
		Status:  http.StatusOK,
		Message: proto.String("Users in group"),
	}, nil
}

func (g *GrpcServer) AddUsersToGroup(ctx context.Context, req *v0_groups.AddUsersToGroupReq) (*v0_groups.AddUsersToGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.AddUsersToGroup")
	defer span.End()

	span.SetAttributes(
		attribute.String("group.id", req.GetId()),
		attribute.Int("users.count", len(req.GetUserIds())),
	)

	err := g.svc.AddUsersToGroup(ctx, req.GetId(), req.GetUserIds())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "add users to group failed")
		return nil, g.mapErrorToStatus(err, "add user to group")
	}

	span.SetStatus(otelcodes.Ok, "users added to group successfully")

	return &v0_groups.AddUsersToGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("Users added to group"),
	}, nil
}

func (g *GrpcServer) RemoveUserFromGroup(ctx context.Context, req *v0_groups.RemoveUserFromGroupReq) (*v0_groups.RemoveUserFromGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.RemoveUserFromGroup")
	defer span.End()

	span.SetAttributes(
		attribute.String("group.id", req.GetId()),
		attribute.String("user.id", req.UserId),
	)

	err := g.svc.RemoveUsersFromGroup(ctx, req.GetId(), []string{req.UserId})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "remove user from group failed")
		return nil, g.mapErrorToStatus(err, "remove user from group")
	}

	span.SetStatus(otelcodes.Ok, "user removed from group successfully")

	return &v0_groups.RemoveUserFromGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("User removed from group"),
	}, nil
}

func (g *GrpcServer) ListUserGroups(ctx context.Context, req *v0_groups.ListUserGroupsReq) (*v0_groups.ListUserGroupsResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.ListUserGroups")
	defer span.End()

	span.SetAttributes(attribute.String("user.id", req.GetId()))

	groups, err := g.svc.GetGroupsForUser(ctx, req.GetId())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "list user groups failed")
		return nil, g.mapErrorToStatus(err, "list user groups")
	}

	respGroups := make([]*v0_groups.Group, len(groups))
	for i, group := range groups {
		respGroups[i] = &v0_groups.Group{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        group.Type.String(),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}
	}

	span.SetAttributes(attribute.Int("groups.count", len(groups)))
	span.SetStatus(otelcodes.Ok, "user groups listed successfully")

	return &v0_groups.ListUserGroupsResp{
		Data:    respGroups,
		Status:  http.StatusOK,
		Message: proto.String("User group list"),
	}, nil
}

func (g *GrpcServer) AddUserToGroups(ctx context.Context, req *v0_groups.AddUserToGroupsReq) (*v0_groups.AddUserToGroupsResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcServer.AddUserToGroups")
	defer span.End()

	span.SetAttributes(
		attribute.String("user.id", req.GetId()),
		attribute.Int("groups.count", len(req.GetGroupIds())),
	)

	err := g.svc.UpdateGroupsForUser(ctx, req.GetId(), req.GetGroupIds())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "add user to groups failed")
		return nil, g.mapErrorToStatus(err, "add user to groups")
	}

	span.SetStatus(otelcodes.Ok, "user added to groups successfully")

	return &v0_groups.AddUserToGroupsResp{
		Status:  http.StatusOK,
		Message: proto.String("User groups added"),
	}, nil
}

// mapErrorToStatus maps known errors to gRPC status errors
func (g *GrpcServer) mapErrorToStatus(err error, action string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrGroupNotFound):
		return status.Errorf(codes.NotFound, "group not found")
	case errors.Is(err, ErrDuplicateGroup):
		return status.Errorf(codes.AlreadyExists, "group already exists")
	case errors.Is(err, ErrUserAlreadyInGroup):
		return status.Errorf(codes.AlreadyExists, "user already in group")
	case errors.Is(err, ErrInvalidGroupName):
		return status.Errorf(codes.InvalidArgument, "invalid group name")
	case errors.Is(err, ErrInvalidGroupType):
		return status.Errorf(codes.InvalidArgument, "invalid group type")
	case errors.Is(err, ErrInvalidTenant):
		return status.Errorf(codes.InvalidArgument, "invalid tenant")
	case errors.Is(err, ErrInvalidGroupID):
		return status.Errorf(codes.InvalidArgument, "invalid group id")
	case errors.Is(err, ErrInternalServerError):
		return status.Errorf(codes.Internal, "internal server error")
	default:
		g.logger.Errorf("Unhandled error in %s: %v", action, err)
		return status.Errorf(codes.Internal, "%s failed", action)
	}
}

func NewGrpcServer(svc ServiceInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *GrpcServer {
	return &GrpcServer{
		svc:     svc,
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}
