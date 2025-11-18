// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
)

var _ ServiceInterface = (*Service)(nil)

type Service struct {
	db    DatabaseInterface
	authz AuthorizerInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) ListGroups(ctx context.Context) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListGroups")
	defer span.End()

	return s.db.ListGroups(ctx)
}

func (s *Service) CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.CreateGroup")
	defer span.End()

	if group.ID != "" {
		return nil, ErrInvalidGroupID
	}

	createdGroup, err := s.db.CreateGroup(ctx, group)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateKey) {
			return nil, ErrDuplicateGroup
		}
		return nil, err
	}
	return createdGroup, nil
}

func (s *Service) GetGroup(ctx context.Context, id string) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.GetGroup")
	defer span.End()

	group, err := s.db.GetGroup(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return group, nil
}

func (s *Service) UpdateGroup(ctx context.Context, id string, group *types.Group) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.UpdateGroup")
	defer span.End()

	updated, err := s.db.UpdateGroup(ctx, id, group)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.DeleteGroup")
	defer span.End()

	if err := s.db.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from db: %v", err)
	}
	if err := s.authz.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from authz: %v", err)
	}
	return nil
}

func (s *Service) AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.AddUsersToGroup")
	defer span.End()

	if len(userIDs) == 0 {
		return nil
	}

	if err := s.db.AddUsersToGroup(ctx, groupID, userIDs); err != nil {
		if errors.Is(err, storage.ErrDuplicateKey) {
			return ErrUserAlreadyInGroup
		}
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			return ErrInvalidGroupID
		}
		return fmt.Errorf("failed to add users to group: %v", err)
	}
	return nil
}

func (s *Service) ListUsersInGroup(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListUsersInGroup")
	defer span.End()

	g, err := s.db.ListUsersInGroup(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to list users in group: %w", err)
	}
	return g, nil
}

func (s *Service) RemoveUsersFromGroup(ctx context.Context, groupID string, users []string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.RemoveUsersFromGroup")
	defer span.End()

	if err := s.db.RemoveUsersFromGroup(ctx, groupID, users); err != nil {
		return fmt.Errorf("failed to remove users from group: %w", err)
	}
	return nil
}

func (s *Service) RemoveAllUsersFromGroup(ctx context.Context, groupID string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.RemoveAllUsersFromGroup")
	defer span.End()

	_, err := s.db.RemoveAllUsersFromGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("failed to remove all users from group: %w", err)
	}
	return nil
}

func (s *Service) GetGroupsForUser(ctx context.Context, userID string) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.GetGroupsForUser")
	defer span.End()

	groups, err := s.db.GetGroupsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get groups for user: %w", err)
	}
	return groups, nil
}

func (s *Service) UpdateGroupsForUser(ctx context.Context, userID string, groupIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.UpdateGroupsForUser")
	defer span.End()

	if err := s.db.UpdateGroupsForUser(ctx, userID, groupIDs); err != nil {
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			return ErrInvalidGroupID
		}
		return err
	}
	return nil
}

func (s *Service) RemoveGroupsForUser(ctx context.Context, userID string) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.RemoveGroupsForUser")
	defer span.End()

	_, err := s.db.RemoveGroupsForUser(ctx, userID)
	if err != nil {
		return err
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
