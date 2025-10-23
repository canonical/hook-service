package groups

import (
	"context"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ ServiceInterface = (*Service)(nil)

type Service struct {
	db    DatabaseInterface
	authz AuthorizerInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) ListGroups(ctx context.Context) ([]*Group, error) {
	return s.db.ListGroups(ctx)
}

func (s *Service) CreateGroup(ctx context.Context, name, organization, description string, gType groupType) (*Group, error) {
	group := &Group{
		Name:         name,
		Organization: organization,
		Description:  description,
		Type:         gType,
	}

	return s.db.CreateGroup(ctx, group)
}

func (s *Service) GetGroup(ctx context.Context, id string) (*Group, error) {
	return s.db.GetGroup(ctx, id)
}

func (s *Service) UpdateGroup(ctx context.Context, id string, group *Group) (*Group, error) {
	return s.db.UpdateGroup(ctx, id, group)
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	if err := s.db.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from db: %w", err)
	}
	if err := s.authz.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from authz: %w", err)
	}
	return nil
}

func (s *Service) AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error {
	if err := s.db.AddUsersToGroup(ctx, groupID, userIDs); err != nil {
		return fmt.Errorf("failed to add users to group: %w", err)
	}
	return nil
}

func (s *Service) ListUsersInGroup(ctx context.Context, groupID string) ([]string, error) {
	return s.db.ListUsersInGroup(ctx, groupID)
}

func (s *Service) RemoveUsersFromGroup(ctx context.Context, groupID string, users []string) error {
	return s.db.RemoveUsersFromGroup(ctx, groupID, users)
}

func (s *Service) RemoveAllUsersFromGroup(ctx context.Context, groupID string) error {
	_, err := s.db.RemoveAllUsersFromGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("failed to remove users from group: %w", err)
	}
	return nil
}

func (s *Service) GetGroupsForUser(ctx context.Context, userID string) ([]*Group, error) {
	return s.db.GetGroupsForUser(ctx, userID)
}

func (s *Service) UpdateGroupsForUser(ctx context.Context, userID string, groupIDs []string) error {
	err := s.db.UpdateGroupsForUser(ctx, userID, groupIDs)
	if err != nil {
		return fmt.Errorf("failed to get groups for user: %w", err)
	}
	return nil
}

func (s *Service) RemoveGroupsForUser(ctx context.Context, userID string) error {
	_, err := s.db.RemoveGroupsForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get groups to remove for user: %w", err)
	}
	return nil
}

func NewService(
	db DatabaseInterface,
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
