package authorization

import (
	"context"
	"errors"
	"net/http"

	v0_authz "github.com/canonical/identity-platform-api/v0/authorization"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

// GrpcServer is the gRPC server for the authorization service.
type GrpcServer struct {
	svc ServiceInterface
	v0_authz.UnimplementedAppAuthorizationServiceServer

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// GetAllowedAppsInGroup handles the gRPC request to get allowed apps in a group.
func (g *GrpcServer) GetAllowedAppsInGroup(ctx context.Context, req *v0_authz.GetAllowedAppsInGroupReq, opts ...grpc.CallOption) (*v0_authz.GetAllowedAppsInGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.GetAllowedAppsInGroup")
	defer span.End()

	if req.GetGroupId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group id is empty")
	}

	apps, err := g.svc.GetAllowedAppsInGroup(ctx, req.GroupId)
	if errors.Is(err, errGroupNotFound) {
		g.logger.Debugf("group not found: %v", req.GetGroupId())
		return nil, status.Errorf(codes.NotFound, "group not found: %v", req.GetGroupId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get allowed apps in group: %v", err)
	}

	respApps := appsToProto(apps)

	msg := "Allowed apps for group"
	return &v0_authz.GetAllowedAppsInGroupResp{
		Data:    respApps,
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

// AddAllowedAppToGroup handles the gRPC request to add an allowed app to a group.
func (g *GrpcServer) AddAllowedAppToGroup(ctx context.Context, req *v0_authz.AddAllowedAppToGroupReq, opts ...grpc.CallOption) (*v0_authz.AddAllowedAppToGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.AddAllowedAppToGroup")
	defer span.End()

	app := req.GetApp()
	if app == nil || app.GetClientId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "app is empty")
	}

	if req.GetGroupId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group id is empty")
	}

	err := g.svc.AddAllowedAppToGroup(ctx, req.GroupId, app.GetClientId())
	if errors.Is(err, errGroupNotFound) {
		g.logger.Debugf("group not found: %v", req.GetGroupId())
		return nil, status.Errorf(codes.NotFound, "group not found: %v", req.GetGroupId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "add allowed app to group: %v", err)
	}

	msg := "App added to allowed list in group"
	return &v0_authz.AddAllowedAppToGroupResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

// RemoveAllowedAppFromGroup handles the gRPC request to remove an allowed app from a group.
func (g *GrpcServer) RemoveAllowedAppFromGroup(ctx context.Context, req *v0_authz.RemoveAllowedAppFromGroupReq, opts ...grpc.CallOption) (*v0_authz.RemoveAllowedAppFromGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveAllowedAppFromGroup")
	defer span.End()

	if req.GetGroupId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group id is empty")
	}
	if req.GetAppId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "app id is empty")
	}

	err := g.svc.RemoveAllowedAppFromGroup(ctx, req.GroupId, req.AppId)
	if errors.Is(err, errAppDoesNotExistInGroup) {
		g.logger.Debugf("app not in group: %v", req.GetAppId())
		return nil, status.Errorf(codes.NotFound, "app not in group: %v", req.GetAppId())
	}
	if errors.Is(err, errGroupNotFound) {
		g.logger.Debugf("group not found: %v", req.GetGroupId())
		return nil, status.Errorf(codes.NotFound, "group not found: %v", req.GetGroupId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remove allowed app from group: %v", err)
	}

	msg := "App removed from allowed list in group"
	return &v0_authz.RemoveAllowedAppFromGroupResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

// RemoveAllowedAppsFromGroup handles the gRPC request to remove all allowed apps from a group.
func (g *GrpcServer) RemoveAllowedAppsFromGroup(ctx context.Context, req *v0_authz.RemoveAllowedAppsFromGroupReq, opts ...grpc.CallOption) (*v0_authz.RemoveAllowedAppsFromGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveAllowedAppsFromGroup")
	defer span.End()

	if req.GetGroupId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "group id is empty")
	}

	err := g.svc.RemoveAllAllowedAppsFromGroup(ctx, req.GroupId)
	if errors.Is(err, errGroupNotFound) {
		g.logger.Debugf("group not found: %v", req.GetGroupId())
		return nil, status.Errorf(codes.NotFound, "group not found: %v", req.GetGroupId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remove allowed apps from group: %v", err)
	}

	msg := "All apps removed from allowed list in group"
	return &v0_authz.RemoveAllowedAppsFromGroupResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

// GetAllowedGroupsForApp handles the gRPC request to get allowed groups for an app.
func (g *GrpcServer) GetAllowedGroupsForApp(ctx context.Context, req *v0_authz.GetAllowedGroupsForAppReq, opts ...grpc.CallOption) (*v0_authz.GetAllowedGroupsForAppResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.GetAllowedGroupsForApp")
	defer span.End()

	if req.GetAppId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "app id is empty")
	}

	groups, err := g.svc.GetAllowedGroupsForApp(ctx, req.AppId)
	if errors.Is(err, errAppDoesNotExist) {
		g.logger.Debugf("app not found: %v", req.GetAppId())
		return nil, status.Errorf(codes.NotFound, "app not found: %v", req.GetAppId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get allowed groups for app: %v", err)
	}

	gg := groupsToProto(groups)

	msg := "List of groups allowed for app"
	return &v0_authz.GetAllowedGroupsForAppResp{
		Data:    gg,
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

// RemoveAllowedGroupsForApp handles the gRPC request to remove all allowed groups for an app.
func (g *GrpcServer) RemoveAllowedGroupsForApp(ctx context.Context, req *v0_authz.RemoveAllowedGroupsForAppReq, opts ...grpc.CallOption) (*v0_authz.RemoveAllowedGroupsForAppResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveAllowedGroupsForApp")
	defer span.End()

	if req.GetAppId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "app id is empty")
	}

	err := g.svc.RemoveAllAllowedGroupsForApp(ctx, req.AppId)
	if errors.Is(err, errAppDoesNotExist) {
		g.logger.Debugf("app not found: %v", req.GetAppId())
		return nil, status.Errorf(codes.NotFound, "app not found: %v", req.GetAppId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remove allowed groups for app: %v", err)
	}

	msg := "All groups removed from allowed list for app"
	return &v0_authz.RemoveAllowedGroupsForAppResp{
		Status:  http.StatusOK,
		Message: &msg,
	}, nil
}

// NewGrpcServer creates a new gRPC server.
func NewGrpcServer(svc ServiceInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *GrpcServer {
	return &GrpcServer{
		svc:     svc,
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}

// appsToProto converts a slice of client IDs into []*v0_authz.App
func appsToProto(apps []string) []*v0_authz.App {
	out := make([]*v0_authz.App, 0, len(apps))
	for _, a := range apps {
		out = append(out, &v0_authz.App{ClientId: a})
	}
	return out
}

// groupsToProto converts a slice of group IDs into []*v0_authz.Group
func groupsToProto(groups []string) []*v0_authz.Group {
	out := make([]*v0_authz.Group, 0, len(groups))
	for _, g := range groups {
		out = append(out, &v0_authz.Group{GroupId: g})
	}
	return out
}
