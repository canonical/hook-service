package groups

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ v0_groups.AuthzGroupsServiceClient = (*GrpcServer)(nil)

type GrpcServer struct {
	svc ServiceInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (g *GrpcServer) CreateGroup(ctx context.Context, req *v0_groups.CreateGroupReq, opts ...grpc.CallOption) (*v0_groups.CreateGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.CreateGroup")
	defer span.End()

	if req.Group == nil {
		return nil, status.Errorf(codes.InvalidArgument, "group cannot be nil")
	}

	gType, err := parseGroupType(req.Group.GetType())
	if err != nil {
		return nil, mapErrorToStatus(err, "create group")
	}

	group, err := g.svc.CreateGroup(ctx, req.Group.Name, "default", req.Group.GetDescription(), gType)
	if err != nil {
		return nil, mapErrorToStatus(err, "create group")
	}

	msg := "Group created"
	return &v0_groups.CreateGroupResp{
		Data: []*v0_groups.Group{{
			Id:           group.ID,
			Name:         group.Name,
			Organization: group.Organization,
			Description:  group.Description,
			Type:         string(group.Type),
			CreatedAt:    timestamppb.New(group.CreatedAt),
			UpdatedAt:    timestamppb.New(group.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) GetGroup(ctx context.Context, req *v0_groups.GetGroupReq, opts ...grpc.CallOption) (*v0_groups.GetGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.GetGroup")
	defer span.End()

	groupID := req.GetId()
	if groupID == "" {
		return nil, mapErrorToStatus(NewValidationError("group_id", "cannot be empty", "GetGroup"), "")
	}

	group, err := g.svc.GetGroup(ctx, groupID)
	if err != nil {
		return nil, mapErrorToStatus(err, "get group")
	}

	msg := "Group details"
	return &v0_groups.GetGroupResp{
		Data: []*v0_groups.Group{{
			Id:           group.ID,
			Name:         group.Name,
			Organization: group.Organization,
			Description:  group.Description,
			Type:         string(group.Type),
			CreatedAt:    timestamppb.New(group.CreatedAt),
			UpdatedAt:    timestamppb.New(group.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) ListGroups(ctx context.Context, req *v0_groups.ListGroupsReq, opts ...grpc.CallOption) (*v0_groups.ListGroupsResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.ListGroups")
	defer span.End()

	groups, err := g.svc.ListGroups(ctx)
	if err != nil {
		return nil, mapErrorToStatus(err, "list groups")
	}

	respGroups := make([]*v0_groups.Group, len(groups))
	for i, group := range groups {
		respGroups[i] = &v0_groups.Group{
			Id:           group.ID,
			Name:         group.Name,
			Organization: group.Organization,
			Description:  group.Description,
			Type:         string(group.Type),
			CreatedAt:    timestamppb.New(group.CreatedAt),
			UpdatedAt:    timestamppb.New(group.UpdatedAt),
		}
	}

	msg := "Group list"
	return &v0_groups.ListGroupsResp{
		Data:    respGroups,
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) RemoveGroup(ctx context.Context, req *v0_groups.RemoveGroupReq, opts ...grpc.CallOption) (*v0_groups.RemoveGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveGroup")
	defer span.End()

	err := g.svc.DeleteGroup(ctx, req.Id)
	if err != nil {
		return nil, mapErrorToStatus(err, "delete group")
	}

	msg := "Group deleted"
	return &v0_groups.RemoveGroupResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) UpdateGroup(ctx context.Context, req *v0_groups.UpdateGroupReq, opts ...grpc.CallOption) (*v0_groups.UpdateGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.UpdateGroup")
	defer span.End()

	if req.Group == nil {
		return nil, status.Errorf(codes.InvalidArgument, "group cannot be nil")
	}

	if req.Group.GetName() != "" {
		return nil, status.Errorf(codes.InvalidArgument, "group name cannot be updated")
	}

	gType, err := parseGroupType(req.Group.GetType())
	if err != nil {
		return nil, mapErrorToStatus(err, "update group")
	}

	group := &Group{
		ID:           req.GetId(),
		Description:  req.Group.GetDescription(),
		Type:         gType,
		Organization: "default",
	}

	gg, err := g.svc.UpdateGroup(ctx, req.GetId(), group)
	if err != nil {
		return nil, mapErrorToStatus(err, "update group")
	}

	msg := "Group updated"
	return &v0_groups.UpdateGroupResp{
		Data: []*v0_groups.Group{{
			Id:           gg.ID,
			Name:         gg.Name,
			Organization: gg.Organization,
			Description:  gg.Description,
			Type:         string(gg.Type),
			CreatedAt:    timestamppb.New(gg.CreatedAt),
			UpdatedAt:    timestamppb.New(gg.UpdatedAt),
		}},
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) ListUsersInGroup(ctx context.Context, req *v0_groups.ListUsersInGroupReq, opts ...grpc.CallOption) (*v0_groups.ListUsersInGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.ListUsersInGroup")
	defer span.End()

	users, err := g.svc.ListUsersInGroup(ctx, req.GetId())
	if err != nil {
		return nil, mapErrorToStatus(err, "list users in group")
	}

	respUsers := make([]*v0_groups.User, len(users))
	for i, user := range users {
		respUsers[i] = &v0_groups.User{Id: user}
	}

	msg := "Users in group"
	return &v0_groups.ListUsersInGroupResp{
		Data:    respUsers,
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) AddUsersToGroup(ctx context.Context, req *v0_groups.AddUsersToGroupReq, opts ...grpc.CallOption) (*v0_groups.AddUsersToGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.AddUserToGroup")
	defer span.End()

	err := g.svc.AddUsersToGroup(ctx, req.GetId(), req.GetUserIds())
	if err != nil {
		return nil, mapErrorToStatus(err, "add user to group")
	}

	msg := "Users added to group"
	return &v0_groups.AddUsersToGroupResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) RemoveUserFromGroup(ctx context.Context, req *v0_groups.RemoveUserFromGroupReq, opts ...grpc.CallOption) (*v0_groups.RemoveUserFromGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveUserFromGroup")
	defer span.End()

	err := g.svc.RemoveUsersFromGroup(ctx, req.GetId(), []string{req.UserId})
	if err != nil {
		return nil, mapErrorToStatus(err, "remove user from group")
	}

	msg := "User removed from group"
	return &v0_groups.RemoveUserFromGroupResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) ListUserGroups(ctx context.Context, req *v0_groups.ListUserGroupsReq, opts ...grpc.CallOption) (*v0_groups.ListUserGroupsResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.ListUserGroups")
	defer span.End()

	groups, err := g.svc.GetGroupsForUser(ctx, req.GetId())
	if err != nil {
		return nil, mapErrorToStatus(err, "list user groups")
	}

	respGroups := make([]*v0_groups.Group, len(groups))
	for i, group := range groups {
		respGroups[i] = &v0_groups.Group{
			Id:           group.ID,
			Name:         group.Name,
			Organization: group.Organization,
			Description:  group.Description,
			Type:         string(group.Type),
			CreatedAt:    timestamppb.New(group.CreatedAt),
			UpdatedAt:    timestamppb.New(group.UpdatedAt),
		}
	}

	msg := "User group list"
	return &v0_groups.ListUserGroupsResp{
		Data:    respGroups,
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func (g *GrpcServer) AddUserToGroups(ctx context.Context, req *v0_groups.AddUserToGroupsReq, opts ...grpc.CallOption) (*v0_groups.AddUserToGroupsResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.AddUserToGroups")
	defer span.End()

	err := g.svc.UpdateGroupsForUser(ctx, req.GetId(), req.GetGroupIds())
	if err != nil {
		return nil, mapErrorToStatus(err, "add user to groups")
	}

	msg := "User groups added"
	return &v0_groups.AddUserToGroupsResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

func NewGrpcServer(svc ServiceInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *GrpcServer {
	return &GrpcServer{
		svc:     svc,
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}

// formatMetadata converts a metadata map to a sorted string representation
func formatMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}

	// Get all keys
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build metadata string
	pairs := make([]string, 0, len(metadata))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, metadata[k]))
	}
	return strings.Join(pairs, ", ")
}

// mapErrorToStatus maps known errors to gRPC status errors
func mapErrorToStatus(err error, action string) error {
	if err == nil {
		return nil
	}

	var gerr *GroupError
	if errors.As(err, &gerr) {
		// Use the operation from the error if available, otherwise use the provided action
		op := gerr.Op
		if op == "" {
			op = action
		}

		var code codes.Code
		switch gerr.Code {
		case ErrCodeGroupNotFound, ErrCodeUserNotFound:
			code = codes.NotFound
		case ErrCodeDuplicateGroup, ErrCodeUserAlreadyInGroup:
			code = codes.AlreadyExists
		case ErrCodeInvalidGroupName:
			code = codes.InvalidArgument
		case ErrCodeUserNotInGroup:
			code = codes.FailedPrecondition
		default:
			code = codes.Internal
		}

		msg := gerr.Message
		if len(gerr.Metadata) > 0 {
			// Include relevant metadata in the error message
			msg = fmt.Sprintf("%s (%s)", msg, formatMetadata(gerr.Metadata))
		}
		return status.Errorf(code, "%s: %s", op, msg)
	}

	// Fallback for non-GroupError errors
	return status.Errorf(codes.Internal, "%s: %v", action, err)
}
