package groups

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ ServiceInterface = (*Service)(nil)

type Service struct {
	db    GroupDatabaseInterface
	authz AuthorizationServiceInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) ListGroups(ctx context.Context) ([]*Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListGroups")
	defer span.End()

	groups, err := s.db.ListGroups(ctx)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return groups, nil
}

func (s *Service) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.GetGroup")
	defer span.End()

	group, err := s.db.GetGroup(ctx, groupID)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return group, nil
}

func (s *Service) GetGroupByName(ctx context.Context, name string) (*Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.GetGroupByName")
	defer span.End()

	group, err := s.db.GetGroupByName(ctx, name)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return group, nil
}

func (s *Service) CreateGroup(ctx context.Context, name string) (*Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.CreateGroup")
	defer span.End()

	organization := "default"
	group, err := s.db.CreateGroup(ctx, name, organization)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return group, nil
}

func (s *Service) DeleteGroup(ctx context.Context, groupID string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.DeleteGroup")
	defer span.End()

	if err := s.db.DeleteGroup(ctx, groupID); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	if err := s.authz.RemoveAllowedApps(ctx, groupID); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	return nil
}

func (s *Service) ListGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListGroupMembers")
	defer span.End()

	users, err := s.db.ListGroupMembers(ctx, groupID)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return users, nil
}

func (s *Service) AddGroupMember(ctx context.Context, groupID string, userID string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.AddGroupMember")
	defer span.End()

	if err := s.db.AddGroupMember(ctx, groupID, userID); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	return nil
}

func (s *Service) RemoveGroupMember(ctx context.Context, groupID string, userID string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.RemoveGroupMember")
	defer span.End()

	if err := s.db.RemoveGroupMember(ctx, groupID, userID); err != nil {
		s.logger.Error(err.Error())
		return err
	}

	return nil
}

func (s *Service) ListUserGroups(ctx context.Context, userID string) ([]*Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListUserGroups")
	defer span.End()

	groups, err := s.db.ListUserGroups(ctx, userID)
	if err != nil {
		s.logger.Error(err.Error())
		return nil, err
	}

	return groups, nil
}

func (s *Service) SetAuthorizer(authz AuthorizationServiceInterface) {
	s.authz = authz
}

func NewServiceWithoutAuthorizer(
	db GroupDatabaseInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.db = db

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}

func NewService(
	db GroupDatabaseInterface,
	authz AuthorizationServiceInterface,
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
