// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"net/http"

	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"github.com/gogo/protobuf/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
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
	if req.Group == nil {
		return nil, status.Errorf(codes.InvalidArgument, "group cannot be nil")
	}

	gType, err := parseGroupType(req.Group.GetType())
	if err != nil {
		return nil, g.mapErrorToStatus(err, "create group")
	}

	group := &Group{
		Name:        req.Group.GetName(),
		TenantId:    DefaultTenantID,
		Description: req.Group.GetDescription(),
		Type:        gType,
	}

	group, err = g.svc.CreateGroup(ctx, group)
	if err != nil {
		return nil, g.mapErrorToStatus(err, "create group")
	}

	return &v0_groups.CreateGroupResp{
		Data: []*v0_groups.Group{{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        string(group.Type),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: proto.String("Group created"),
	}, nil
}

func (g *GrpcServer) GetGroup(ctx context.Context, req *v0_groups.GetGroupReq) (*v0_groups.GetGroupResp, error) {
	group, err := g.svc.GetGroup(ctx, req.Id)
	if err != nil {
		return nil, g.mapErrorToStatus(err, "get group")
	}

	return &v0_groups.GetGroupResp{
		Data: []*v0_groups.Group{{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        string(group.Type),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: proto.String("Group details"),
	}, nil
}

func (g *GrpcServer) ListGroups(ctx context.Context, req *v0_groups.ListGroupsReq) (*v0_groups.ListGroupsResp, error) {
	groups, err := g.svc.ListGroups(ctx)
	if err != nil {
		return nil, g.mapErrorToStatus(err, "list groups")
	}

	respGroups := make([]*v0_groups.Group, len(groups))
	for i, group := range groups {
		respGroups[i] = &v0_groups.Group{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        string(group.Type),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}
	}
	return &v0_groups.ListGroupsResp{
		Data:    respGroups,
		Status:  http.StatusOK,
		Message: proto.String("Group list"),
	}, nil
}

func (g *GrpcServer) RemoveGroup(ctx context.Context, req *v0_groups.RemoveGroupReq) (*v0_groups.RemoveGroupResp, error) {
	err := g.svc.DeleteGroup(ctx, req.Id)
	if err != nil {
		return nil, g.mapErrorToStatus(err, "delete group")
	}

	return &v0_groups.RemoveGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("Group deleted"),
	}, nil
}

func (g *GrpcServer) UpdateGroup(ctx context.Context, req *v0_groups.UpdateGroupReq) (*v0_groups.UpdateGroupResp, error) {
	if req.Group == nil {
		return nil, status.Errorf(codes.InvalidArgument, "group cannot be nil")
	}

	if req.Group.GetName() != "" {
		return nil, status.Errorf(codes.InvalidArgument, "group name cannot be updated")
	}

	gType, err := parseGroupType(req.Group.GetType())
	if err != nil {
		return nil, g.mapErrorToStatus(err, "update group")
	}

	group := &Group{
		Description: req.Group.GetDescription(),
		Type:        gType,
		TenantId:    DefaultTenantID,
	}

	gg, err := g.svc.UpdateGroup(ctx, req.GetId(), group)
	if err != nil {
		return nil, g.mapErrorToStatus(err, "update group")
	}

	return &v0_groups.UpdateGroupResp{
		Data: []*v0_groups.Group{{
			Id:          gg.ID,
			Name:        gg.Name,
			TenantId:    gg.TenantId,
			Description: gg.Description,
			Type:        string(gg.Type),
			CreatedAt:   timestamppb.New(gg.CreatedAt),
			UpdatedAt:   timestamppb.New(gg.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: proto.String("Group updated"),
	}, nil
}

func (g *GrpcServer) ListUsersInGroup(ctx context.Context, req *v0_groups.ListUsersInGroupReq) (*v0_groups.ListUsersInGroupResp, error) {
	users, err := g.svc.ListUsersInGroup(ctx, req.GetId())
	if err != nil {
		return nil, g.mapErrorToStatus(err, "list users in group")
	}

	respUsers := make([]*v0_groups.User, len(users))
	for i, user := range users {
		respUsers[i] = &v0_groups.User{Id: user}
	}

	return &v0_groups.ListUsersInGroupResp{
		Data:    respUsers,
		Status:  http.StatusOK,
		Message: proto.String("Users in group"),
	}, nil
}

func (g *GrpcServer) AddUsersToGroup(ctx context.Context, req *v0_groups.AddUsersToGroupReq) (*v0_groups.AddUsersToGroupResp, error) {
	err := g.svc.AddUsersToGroup(ctx, req.GetId(), req.GetUserIds())
	if err != nil {
		return nil, g.mapErrorToStatus(err, "add user to group")
	}

	return &v0_groups.AddUsersToGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("Users added to group"),
	}, nil
}

func (g *GrpcServer) RemoveUserFromGroup(ctx context.Context, req *v0_groups.RemoveUserFromGroupReq) (*v0_groups.RemoveUserFromGroupResp, error) {
	err := g.svc.RemoveUsersFromGroup(ctx, req.GetId(), []string{req.UserId})
	if err != nil {
		return nil, g.mapErrorToStatus(err, "remove user from group")
	}

	return &v0_groups.RemoveUserFromGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("User removed from group"),
	}, nil
}

func (g *GrpcServer) ListUserGroups(ctx context.Context, req *v0_groups.ListUserGroupsReq) (*v0_groups.ListUserGroupsResp, error) {
	groups, err := g.svc.GetGroupsForUser(ctx, req.GetId())
	if err != nil {
		return nil, g.mapErrorToStatus(err, "list user groups")
	}

	respGroups := make([]*v0_groups.Group, len(groups))
	for i, group := range groups {
		respGroups[i] = &v0_groups.Group{
			Id:          group.ID,
			Name:        group.Name,
			TenantId:    group.TenantId,
			Description: group.Description,
			Type:        string(group.Type),
			CreatedAt:   timestamppb.New(group.CreatedAt),
			UpdatedAt:   timestamppb.New(group.UpdatedAt),
		}
	}

	return &v0_groups.ListUserGroupsResp{
		Data:    respGroups,
		Status:  http.StatusOK,
		Message: proto.String("User group list"),
	}, nil
}

func (g *GrpcServer) AddUserToGroups(ctx context.Context, req *v0_groups.AddUserToGroupsReq) (*v0_groups.AddUserToGroupsResp, error) {
	err := g.svc.UpdateGroupsForUser(ctx, req.GetId(), req.GetGroupIds())
	if err != nil {
		return nil, g.mapErrorToStatus(err, "add user to groups")
	}

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
	case errors.Is(err, ErrInvalidGroupName):
		return status.Errorf(codes.InvalidArgument, "invalid group name")
	case errors.Is(err, ErrInvalidGroupType):
		return status.Errorf(codes.InvalidArgument, "invalid group type")
	case errors.Is(err, ErrInvalidTenant):
		return status.Errorf(codes.InvalidArgument, "invalid tenant")
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
