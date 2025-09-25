package authorization

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/groups"
)

type Service struct {
	db     AuthorizationDatabaseInterface
	groups groups.ServiceInterface
	authz  AuthorizerInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) GetAllowedApps(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.GetAllowedApps")
	defer span.End()

	group, err := s.groups.GetGroup(ctx, groupID) // Check if group exists
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}
	if group == nil {
		return nil, NO_MAPPING_FOR_GROUP
	}

	apps, err := s.db.GetAllowedApps(ctx, groupID)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return apps, nil
}

func (s *Service) AddAllowedApp(ctx context.Context, groupID string, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.AddAllowedApp")
	defer span.End()

	group, err := s.groups.GetGroup(ctx, groupID) // Check if group exists
	if err != nil {
		s.logger.Error(err.Error())
		return err
	}
	if group == nil {
		return NO_MAPPING_FOR_GROUP
	}

	if err := s.db.AddAllowedApp(ctx, groupID, app); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	err = s.authz.AddAllowedAppToGroup(ctx, groupID, app)
	if err != nil {
		s.db.RemoveAllowedApp(ctx, groupID, app) // Rollback
		s.logger.Error(err.Error())
		return err
	}

	return nil
}

func (s *Service) RemoveAllowedApps(ctx context.Context, groupID string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllowedApps")
	defer span.End()

	group, err := s.groups.GetGroup(ctx, groupID) // Check if group exists
	if err != nil {
		s.logger.Error(err.Error())
		return err
	}
	if group == nil {
		return NO_MAPPING_FOR_GROUP
	}

	apps, err := s.db.GetAllowedApps(ctx, groupID)
	if err != nil {
		s.logger.Error(err.Error())
		return err
	}

	if err := s.db.RemoveAllowedApps(ctx, groupID); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	for _, app := range apps {
		err = s.authz.RemoveAllowedAppFromGroup(ctx, groupID, app)
		if err != nil {
			s.logger.Error(err.Error())
			return err
		}
	}

	return nil
}

func (s *Service) RemoveAllowedApp(ctx context.Context, groupID string, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllowedApp")
	defer span.End()

	group, err := s.groups.GetGroup(ctx, groupID) // Check if group exists
	if err != nil {
		s.logger.Error(err.Error())
		return err
	}
	if group == nil {
		return NO_MAPPING_FOR_GROUP
	}

	if err := s.db.RemoveAllowedApp(ctx, groupID, app); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	err = s.authz.RemoveAllowedAppFromGroup(ctx, groupID, app)
	if err != nil {
		s.db.AddAllowedApp(ctx, groupID, app) // Rollback
		s.logger.Error(err.Error())
		return err
	}

	return nil
}

func (s *Service) GetAllowedGroupsForApp(ctx context.Context, app string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.GetAllowedGroupsForApp")
	defer span.End()

	groups, err := s.db.GetAllowedGroupsForApp(ctx, app)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return groups, nil
}

func (s *Service) RemoveAllowedGroupsForApp(ctx context.Context, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllowedGroupsForApp")
	defer span.End()

	groups, err := s.db.GetAllowedGroupsForApp(ctx, app)
	if err != nil {
		s.logger.Error(err.Error())
		return err
	}

	for _, groupID := range groups {
		if err := s.db.RemoveAllowedApp(ctx, groupID, app); err != nil {
			s.logger.Error(err.Error())
			return err
		}

	}
	err = s.authz.RemoveAllowedGroupsForApp(ctx, app)
	if err != nil {
		s.logger.Error(err.Error())
		return err
	}

	return nil
}

func NewService(
	db AuthorizationDatabaseInterface,
	groups groups.ServiceInterface,
	authz AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.db = db
	s.groups = groups
	s.authz = authz

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}
