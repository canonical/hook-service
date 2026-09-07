// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package groups

import (
	"context"
	"errors"
	"fmt"

	v1 "github.com/canonical/hook-service/gen/authorization/service/api/v1"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
	"github.com/canonical/hook-service/pkg/authentication"
)

var _ ServiceInterface = (*Service)(nil)

type Service struct {
	db        DatabaseInterface
	authz     AuthorizerInterface
	publisher PermissionPublisherInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (s *Service) ListGroups(ctx context.Context) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "groups.Service.ListGroups")
	defer span.End()

	groups, err := s.db.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}
	return groups, nil
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

	if creatorID := authentication.UserIDFromContext(ctx); creatorID != "" {
		if err := s.db.AddGroupOwner(ctx, createdGroup.ID, creatorID); err != nil {
			return nil, fmt.Errorf("failed to record group owner: %w", err)
		}
		if s.publisher != nil {
			subject := fmt.Sprintf("user:%s", creatorID)
			object := fmt.Sprintf("group-in-claim:%s", createdGroup.ID)
			if err := s.publisher.PublishWrite(ctx, subject, "owner", object); err != nil {
				s.logger.Warnf("failed to publish owner permission write for group %s: %v", createdGroup.ID, err)
			}
		}
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

	members, err := s.db.ListUsersInGroup(ctx, id)
	if err != nil {
		s.logger.Warnf("failed to list users in group before deletion: %v", err)
	}
	owners, err := s.db.ListOwnersInGroup(ctx, id)
	if err != nil {
		s.logger.Warnf("failed to list owners in group before deletion: %v", err)
	}

	if err := s.db.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from db: %v", err)
	}
	if err := s.authz.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("failed to delete group from authz: %v", err)
	}

	if s.publisher != nil {
		object := fmt.Sprintf("group-in-claim:%s", id)
		ops := make([]Operation, 0, len(members)+len(owners))
		for _, member := range members {
			ops = append(ops, Operation{
				Op:       v1.PermissionOp_PERMISSION_OP_DELETE,
				Subject:  fmt.Sprintf("user:%s", member),
				Relation: "member",
				Object:   object,
			})
		}
		for _, owner := range owners {
			ops = append(ops, Operation{
				Op:       v1.PermissionOp_PERMISSION_OP_DELETE,
				Subject:  fmt.Sprintf("user:%s", owner),
				Relation: "owner",
				Object:   object,
			})
		}
		if len(ops) > 0 {
			if err := s.publisher.PublishOperations(ctx, ops...); err != nil {
				s.logger.Warnf("failed to publish permission deletes for group %s: %v", id, err)
			}
		}
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
		if errors.Is(err, storage.ErrForeignKeyViolation) {
			return ErrInvalidGroupID
		}
		return fmt.Errorf("failed to add users to group: %v", err)
	}

	if s.publisher != nil {
		object := fmt.Sprintf("group-in-claim:%s", groupID)
		ops := make([]Operation, 0, len(userIDs))
		for _, userID := range userIDs {
			ops = append(ops, Operation{
				Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
				Subject:  fmt.Sprintf("user:%s", userID),
				Relation: "member",
				Object:   object,
			})
		}
		if len(ops) > 0 {
			if err := s.publisher.PublishOperations(ctx, ops...); err != nil {
				s.logger.Warnf("failed to publish member permission writes for group %s: %v", groupID, err)
			}
		}
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

	if s.publisher != nil {
		object := fmt.Sprintf("group-in-claim:%s", groupID)
		ops := make([]Operation, 0, len(users))
		for _, userID := range users {
			ops = append(ops, Operation{
				Op:       v1.PermissionOp_PERMISSION_OP_DELETE,
				Subject:  fmt.Sprintf("user:%s", userID),
				Relation: "member",
				Object:   object,
			})
		}
		if len(ops) > 0 {
			if err := s.publisher.PublishOperations(ctx, ops...); err != nil {
				s.logger.Warnf("failed to publish member permission deletes for group %s: %v", groupID, err)
			}
		}
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

func (s *Service) StreamGroupsForUser(ctx context.Context, tenantID, userID string, fn func(*types.Group) error) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.StreamGroupsForUser")
	defer span.End()

	if err := s.db.StreamGroupsForUser(ctx, tenantID, userID, fn); err != nil {
		return fmt.Errorf("failed to stream groups for user: %w", err)
	}
	return nil
}

func (s *Service) StreamUsersInGroup(ctx context.Context, tenantID, groupID string, fn func(string) error) error {
	ctx, span := s.tracer.Start(ctx, "groups.Service.StreamUsersInGroup")
	defer span.End()

	if err := s.db.StreamUsersInGroup(ctx, tenantID, groupID, fn); err != nil {
		return fmt.Errorf("failed to stream users in group: %w", err)
	}
	return nil
}

func NewService(
	db DatabaseInterface,
	authz AuthorizerInterface,
	publisher PermissionPublisherInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *Service {
	s := new(Service)

	s.db = db
	s.authz = authz
	s.publisher = publisher

	s.monitor = monitor
	s.tracer = tracer
	s.logger = logger

	return s
}
