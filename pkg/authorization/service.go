package authorization

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/storage"
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

	return s.db.GetAllowedApps(ctx, groupID)
}

func (s *Service) AddAllowedAppToGroup(ctx context.Context, groupID string, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.AddAllowedAppToGroup")
	defer span.End()

	if err := s.db.AddAllowedApp(ctx, groupID, app); err != nil {
		if errors.Is(err, storage.ErrDuplicateKey) {
			return errAppAlreadyExistsInGroup
		}
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			return errInvalidGroupID
		}
		return fmt.Errorf("failed to add allowed app to group in db: %v", err)
	}

	if err := s.authz.AddAllowedAppToGroup(ctx, groupID, app); err != nil {
		return fmt.Errorf("failed to add allowed app to group in authz: %v", err)
	}

	return nil
}

func (s *Service) RemoveAllAllowedAppsFromGroup(ctx context.Context, groupID string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllAllowedAppsFromGroup")
	defer span.End()

	_, err := s.db.RemoveAllowedApps(ctx, groupID)
	if err != nil {
		return fmt.Errorf("failed to remove allowed apps from db: %v", err)
	}

	err = s.authz.RemoveAllAllowedAppsFromGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("failed to remove allowed apps from authz: %v", err)
	}

	return nil
}

func (s *Service) RemoveAllowedAppFromGroup(ctx context.Context, groupID string, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllowedAppFromGroup")
	defer span.End()

	if err := s.db.RemoveAllowedApp(ctx, groupID, app); err != nil {
		return fmt.Errorf("failed to remove allowed app from db: %v", err)
	}

	if err := s.authz.RemoveAllowedAppFromGroup(ctx, groupID, app); err != nil {
		return fmt.Errorf("failed to remove allowed app from authz: %v", err)
	}

	return nil
}

func (s *Service) GetAllowedGroupsForApp(ctx context.Context, app string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.GetAllowedGroupsForApp")
	defer span.End()

	groups, err := s.db.GetAllowedGroupsForApp(ctx, app)
	if err != nil {
		return nil, err
	}

	return groups, nil
}

func (s *Service) RemoveAllAllowedGroupsForApp(ctx context.Context, app string) error {
	ctx, span := s.tracer.Start(ctx, "authorization.Service.RemoveAllAllowedGroupsForApp")
	defer span.End()

	_, err := s.db.RemoveAllAllowedGroupsForApp(ctx, app)
	if err != nil {
		return fmt.Errorf("failed to remove allowed groups from db: %v", err)
	}

	if err := s.authz.RemoveAllAllowedGroupsForApp(ctx, app); err != nil {
		return fmt.Errorf("failed to remove allowed groups from authz: %v", err)
	}

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
