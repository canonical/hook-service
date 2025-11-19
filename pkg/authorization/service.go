// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ ServiceInterface = (*Service)(nil)

type Service struct {
	db    AuthorizationDatabaseInterface
	authz AuthorizerInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) GetAllowedAppsInGroup(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.GetAllowedAppsInGroup")
	defer span.End()

	apps, err := s.db.GetAllowedApps(ctx, groupID)
	if err == nil {
		s.logger.Infof("Retrieved %d allowed app(s) for group %s", len(apps), groupID)
	}
	return apps, err
}

func (s *Service) AddAllowedAppToGroup(ctx context.Context, groupID string, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.AddAllowedAppToGroup")
	defer span.End()

	if err := s.db.AddAllowedApp(ctx, groupID, app); err != nil {
		return err
	}

	// TODO: use group name instead when group API is implemented
	if err := s.authz.AddAllowedAppToGroup(ctx, groupID, app); err != nil {
		s.db.RemoveAllowedApp(ctx, groupID, app) // Rollback
		return err
	}

	s.logger.Infof("Added app %s to allowed list for group %s", app, groupID)
	return nil
}

func (s *Service) RemoveAllAllowedAppsFromGroup(ctx context.Context, groupID string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllAllowedAppsFromGroup")
	defer span.End()

	apps, err := s.db.RemoveAllowedApps(ctx, groupID)
	if err != nil {
		return err
	}

	// TODO: use group name instead when group API is implemented
	err = s.authz.RemoveAllAllowedAppsFromGroup(ctx, groupID)
	if err != nil {
		s.db.AddAllowedApps(ctx, groupID, apps) // Rollback
		return err
	}

	s.logger.Infof("Removed %d app(s) from allowed list for group %s", len(apps), groupID)
	return nil
}

func (s *Service) RemoveAllowedAppFromGroup(ctx context.Context, groupID string, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllowedAppFromGroup")
	defer span.End()

	if err := s.db.RemoveAllowedApp(ctx, groupID, app); err != nil {
		return err
	}

	// TODO: use group name instead when group API is implemented
	if err := s.authz.RemoveAllowedAppFromGroup(ctx, groupID, app); err != nil {
		s.db.AddAllowedApp(ctx, groupID, app) // Rollback
		return err
	}

	s.logger.Infof("Removed app %s from allowed list for group %s", app, groupID)
	return nil
}

func (s *Service) GetAllowedGroupsForApp(ctx context.Context, app string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.GetAllowedGroupsForApp")
	defer span.End()

	groups, err := s.db.GetAllowedGroupsForApp(ctx, app)
	if err == nil {
		s.logger.Infof("Retrieved %d allowed group(s) for app %s", len(groups), app)
	}
	return groups, err
}

func (s *Service) RemoveAllAllowedGroupsForApp(ctx context.Context, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllAllowedGroupsForApp")
	defer span.End()

	groups, err := s.db.RemoveAllAllowedGroupsForApp(ctx, app)
	if err != nil {
		return err
	}

	// TODO: use group name instead when group API is implemented
	if err := s.authz.RemoveAllAllowedGroupsForApp(ctx, app); err != nil {
		s.db.AddAllowedGroupsForApp(ctx, app, groups) // Rollback
		return err
	}

	s.logger.Infof("Removed %d group(s) from allowed list for app %s", len(groups), app)
	return nil
}

func NewService(
	db AuthorizationDatabaseInterface,
	authz AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.db = db
	s.authz = authz

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}
