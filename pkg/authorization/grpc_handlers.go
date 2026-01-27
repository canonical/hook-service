// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import (
	"context"
	"errors"
	"net/http"

	v0_authz "github.com/canonical/identity-platform-api/v0/authorization"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ v0_authz.AppAuthorizationServiceServer = (*GrpcServer)(nil)

// GrpcServer is the gRPC server for the authorization service.
type GrpcServer struct {
	svc ServiceInterface
	v0_authz.UnimplementedAppAuthorizationServiceServer

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// GetAllowedAppsInGroup handles the gRPC request to get allowed apps in a group.
func (g *GrpcServer) GetAllowedAppsInGroup(ctx context.Context, req *v0_authz.GetAllowedAppsInGroupReq) (*v0_authz.GetAllowedAppsInGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.GetAllowedAppsInGroup")
	defer span.End()

	span.SetAttributes(attribute.String("group.id", req.GetGroupId()))

	if req.GetGroupId() == "" {
		err := status.Errorf(codes.InvalidArgument, "group id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	g.logger.Debugf("GetAllowedAppsInGroup request for group: %s", req.GetGroupId())

	apps, err := g.svc.GetAllowedAppsInGroup(ctx, req.GroupId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "get allowed apps in group failed")
		return nil, g.mapErrorToStatus(err, "get allowed apps in group")
	}

	respApps := appsToProto(apps)

	span.SetAttributes(attribute.Int("apps.count", len(apps)))
	span.SetStatus(otelcodes.Ok, "allowed apps retrieved successfully")

	return &v0_authz.GetAllowedAppsInGroupResp{
		Data:    respApps,
		Status:  http.StatusOK,
		Message: proto.String("Allowed apps for group"),
	}, nil
}

// AddAllowedAppToGroup handles the gRPC request to add an allowed app to a group.
func (g *GrpcServer) AddAllowedAppToGroup(ctx context.Context, req *v0_authz.AddAllowedAppToGroupReq) (*v0_authz.AddAllowedAppToGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.AddAllowedAppToGroup")
	defer span.End()

	app := req.GetApp()
	span.SetAttributes(
		attribute.String("group.id", req.GetGroupId()),
		attribute.String("app.client_id", app.GetClientId()),
	)

	if app == nil || app.GetClientId() == "" {
		err := status.Errorf(codes.InvalidArgument, "app is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	if req.GetGroupId() == "" {
		err := status.Errorf(codes.InvalidArgument, "group id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	g.logger.Debugf("AddAllowedAppToGroup request for group: %s, app: %s", req.GetGroupId(), app.GetClientId())

	err := g.svc.AddAllowedAppToGroup(ctx, req.GroupId, app.GetClientId())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "add allowed app to group failed")
		return nil, g.mapErrorToStatus(err, "add allowed app to group")
	}

	span.SetStatus(otelcodes.Ok, "app added to allowed list successfully")

	return &v0_authz.AddAllowedAppToGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("App added to allowed list in group"),
	}, nil
}

// RemoveAllowedAppFromGroup handles the gRPC request to remove an allowed app from a group.
func (g *GrpcServer) RemoveAllowedAppFromGroup(ctx context.Context, req *v0_authz.RemoveAllowedAppFromGroupReq) (*v0_authz.RemoveAllowedAppFromGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveAllowedAppFromGroup")
	defer span.End()

	span.SetAttributes(
		attribute.String("group.id", req.GetGroupId()),
		attribute.String("app.id", req.GetAppId()),
	)

	if req.GetGroupId() == "" {
		err := status.Errorf(codes.InvalidArgument, "group id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}
	if req.GetAppId() == "" {
		err := status.Errorf(codes.InvalidArgument, "app id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	g.logger.Debugf("RemoveAllowedAppFromGroup request for group: %s, app: %s", req.GetGroupId(), req.GetAppId())

	err := g.svc.RemoveAllowedAppFromGroup(ctx, req.GroupId, req.AppId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "remove allowed app from group failed")
		return nil, g.mapErrorToStatus(err, "remove allowed app from group")
	}

	span.SetStatus(otelcodes.Ok, "app removed from allowed list successfully")

	return &v0_authz.RemoveAllowedAppFromGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("App removed from allowed list in group"),
	}, nil
}

// RemoveAllowedAppsFromGroup handles the gRPC request to remove all allowed apps from a group.
func (g *GrpcServer) RemoveAllowedAppsFromGroup(ctx context.Context, req *v0_authz.RemoveAllowedAppsFromGroupReq) (*v0_authz.RemoveAllowedAppsFromGroupResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveAllowedAppsFromGroup")
	defer span.End()

	span.SetAttributes(attribute.String("group.id", req.GetGroupId()))

	if req.GetGroupId() == "" {
		err := status.Errorf(codes.InvalidArgument, "group id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	g.logger.Debugf("RemoveAllowedAppsFromGroup request for group: %s", req.GetGroupId())

	err := g.svc.RemoveAllAllowedAppsFromGroup(ctx, req.GroupId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "remove allowed apps from group failed")
		return nil, g.mapErrorToStatus(err, "remove allowed apps from group")
	}

	span.SetStatus(otelcodes.Ok, "all apps removed from allowed list successfully")

	return &v0_authz.RemoveAllowedAppsFromGroupResp{
		Status:  http.StatusOK,
		Message: proto.String("All apps removed from allowed list in group"),
	}, nil
}

// GetAllowedGroupsForApp handles the gRPC request to get allowed groups for an app.
func (g *GrpcServer) GetAllowedGroupsForApp(ctx context.Context, req *v0_authz.GetAllowedGroupsForAppReq) (*v0_authz.GetAllowedGroupsForAppResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.GetAllowedGroupsForApp")
	defer span.End()

	span.SetAttributes(attribute.String("app.id", req.GetAppId()))

	if req.GetAppId() == "" {
		err := status.Errorf(codes.InvalidArgument, "app id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	g.logger.Debugf("GetAllowedGroupsForApp request for app: %s", req.GetAppId())

	groups, err := g.svc.GetAllowedGroupsForApp(ctx, req.AppId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "get allowed groups for app failed")
		return nil, g.mapErrorToStatus(err, "get allowed groups for app")
	}

	gg := groupsToProto(groups)

	span.SetAttributes(attribute.Int("groups.count", len(groups)))
	span.SetStatus(otelcodes.Ok, "allowed groups retrieved successfully")

	return &v0_authz.GetAllowedGroupsForAppResp{
		Data:    gg,
		Status:  http.StatusOK,
		Message: proto.String("List of groups allowed for app"),
	}, nil
}

// RemoveAllowedGroupsForApp handles the gRPC request to remove all allowed groups for an app.
func (g *GrpcServer) RemoveAllowedGroupsForApp(ctx context.Context, req *v0_authz.RemoveAllowedGroupsForAppReq) (*v0_authz.RemoveAllowedGroupsForAppResp, error) {
	ctx, span := g.tracer.Start(ctx, "groups.GrpcHandler.RemoveAllowedGroupsForApp")
	defer span.End()

	span.SetAttributes(attribute.String("app.id", req.GetAppId()))

	if req.GetAppId() == "" {
		err := status.Errorf(codes.InvalidArgument, "app id is empty")
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "invalid argument")
		return nil, err
	}

	g.logger.Debugf("RemoveAllowedGroupsForApp request for app: %s", req.GetAppId())

	err := g.svc.RemoveAllAllowedGroupsForApp(ctx, req.AppId)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "remove allowed groups for app failed")
		return nil, g.mapErrorToStatus(err, "remove allowed groups for app")
	}

	span.SetStatus(otelcodes.Ok, "all groups removed from allowed list successfully")

	return &v0_authz.RemoveAllowedGroupsForAppResp{
		Status:  http.StatusOK,
		Message: proto.String("All groups removed from allowed list for app"),
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

// mapErrorToStatus maps known errors to gRPC status errors
func (g *GrpcServer) mapErrorToStatus(err error, action string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrGroupNotFound):
		return status.Errorf(codes.NotFound, "group not found")
	case errors.Is(err, ErrAppDoesNotExist):
		return status.Errorf(codes.NotFound, "app not found")
	case errors.Is(err, ErrAppDoesNotExistInGroup):
		return status.Errorf(codes.NotFound, "app not in group")
	case errors.Is(err, ErrAppAlreadyExistsInGroup):
		return status.Errorf(codes.AlreadyExists, "app already exists in group")
	case errors.Is(err, ErrInternalServerError):
		return status.Errorf(codes.Internal, "internal server error")
	default:
		g.logger.Errorf("Unhandled error in %s: %v", action, err)
		return status.Errorf(codes.Internal, "%s failed", action)
	}
}
