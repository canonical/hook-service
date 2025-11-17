// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/canonical/hook-service/internal/types"
)

// ListGroups retrieves all groups from the database.
func (s *Storage) ListGroups(ctx context.Context) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.ListGroups")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Select("id", "name", "tenant_id", "description", "type", "created_at", "updated_at").
		From("groups").
		OrderBy("name ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query groups: %v", err)
	}
	defer rows.Close()

	groups := make([]*types.Group, 0)
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group: %v", err)
		}
		groups = append(groups, group)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating groups: %v", err)
	}

	return groups, nil
}

// CreateGroup inserts a new group into the database.
func (s *Storage) CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.CreateGroup")
	defer span.End()

	now := time.Now().UTC()
	var id int64
	var createdAt, updatedAt time.Time

	err := s.db.Statement(ctx).
		Insert("groups").
		Columns("name", "tenant_id", "description", "type", "created_at", "updated_at").
		Values(group.Name, group.TenantId, group.Description, group.Type, now, now).
		Suffix("RETURNING id, created_at, updated_at").
		QueryRowContext(ctx).
		Scan(&id, &createdAt, &updatedAt)
	if err != nil {
		if IsDuplicateKeyError(err) {
			return nil, WrapDuplicateKeyError(err, "group name already exists")
		}
		return nil, fmt.Errorf("failed to insert group: %v", err)
	}

	return &types.Group{
		ID:          fmt.Sprintf("%d", id),
		Name:        group.Name,
		TenantId:    group.TenantId,
		Description: group.Description,
		Type:        group.Type,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

// GetGroup retrieves a single group by ID.
func (s *Storage) GetGroup(ctx context.Context, id string) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.GetGroup")
	defer span.End()

	row := s.db.Statement(ctx).
		Select("id", "name", "tenant_id", "description", "type", "created_at", "updated_at").
		From("groups").
		Where(sq.Eq{"id": id}).
		QueryRowContext(ctx)

	group, err := scanGroup(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query group: %v", err)
	}

	return group, nil
}

// UpdateGroup updates an existing group's mutable fields.
func (s *Storage) UpdateGroup(ctx context.Context, id string, group *types.Group) (*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.UpdateGroup")
	defer span.End()

	now := time.Now().UTC()

	result, err := s.db.Statement(ctx).
		Update("groups").
		Set("name", group.Name).
		Set("description", group.Description).
		Set("type", group.Type).
		Set("updated_at", now).
		Where(sq.Eq{"id": id}).
		ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to update group: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %v", err)
	}

	if rowsAffected == 0 {
		return nil, ErrNotFound
	}

	updated := *group
	updated.ID = id
	updated.UpdatedAt = now

	return &updated, nil
}

// DeleteGroup removes a group from the database.
func (s *Storage) DeleteGroup(ctx context.Context, id string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.DeleteGroup")
	defer span.End()

	_, err := s.db.Statement(ctx).
		Delete("groups").
		Where(sq.Eq{"id": id}).
		ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete group: %v", err)
	}

	return nil
}

// AddUsersToGroup adds multiple users to a group.
func (s *Storage) AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.AddUsersToGroup")
	defer span.End()

	if len(userIDs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	insert := s.db.Statement(ctx).
		Insert("group_members").
		Columns("group_id", "user_id", "tenant_id", "role", "created_at", "updated_at")

	for _, userID := range userIDs {
		insert = insert.Values(groupID, userID, "default", "member", now, now)
	}

	_, err := insert.ExecContext(ctx)
	if err != nil {
		if IsDuplicateKeyError(err) {
			return WrapDuplicateKeyError(err, "user already in group")
		}
		if IsForeignKeyViolation(err) {
			return WrapForeignKeyError(err, "group does not exist")
		}
		return fmt.Errorf("failed to insert group members: %v", err)
	}

	return nil
}

// ListUsersInGroup retrieves all user IDs that are members of a group.
func (s *Storage) ListUsersInGroup(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.ListUsersInGroup")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Select("user_id").
		From("group_members").
		Where(sq.Eq{"group_id": groupID}).
		OrderBy("user_id ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query group members: %v", err)
	}
	defer rows.Close()

	userIDs := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("failed to scan user ID: %v", err)
		}
		userIDs = append(userIDs, userID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group members: %v", err)
	}

	return userIDs, nil
}

// RemoveUsersFromGroup removes specific users from a group.
func (s *Storage) RemoveUsersFromGroup(ctx context.Context, groupID string, users []string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.RemoveUsersFromGroup")
	defer span.End()

	_, err := s.db.Statement(ctx).
		Delete("group_members").
		Where(sq.Eq{"group_id": groupID, "user_id": users}).
		ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to remove users from group: %v", err)
	}

	return nil
}

// RemoveAllUsersFromGroup removes all users from a group and returns the list of removed user IDs.
func (s *Storage) RemoveAllUsersFromGroup(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.RemoveAllUsersFromGroup")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Delete("group_members").
		Where(sq.Eq{"group_id": groupID}).
		Suffix("RETURNING user_id").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to remove all users from group: %v", err)
	}
	defer rows.Close()

	userIDs := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("failed to scan user ID: %v", err)
		}
		userIDs = append(userIDs, userID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating removed users: %v", err)
	}

	return userIDs, nil
}

// GetGroupsForUser retrieves all groups that a user belongs to.
func (s *Storage) GetGroupsForUser(ctx context.Context, userID string) ([]*types.Group, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.GetGroupsForUser")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Select("g.id", "g.name", "g.tenant_id", "g.description", "g.type", "g.created_at", "g.updated_at").
		From("groups g").
		Join("group_members gm ON g.id = gm.group_id").
		Where(sq.Eq{"gm.user_id": userID}).
		OrderBy("g.name ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query groups for user: %v", err)
	}
	defer rows.Close()

	groups := make([]*types.Group, 0)
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group: %v", err)
		}
		groups = append(groups, group)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating groups: %v", err)
	}

	return groups, nil
}

// UpdateGroupsForUser replaces all group memberships for a user with the specified groups.
func (s *Storage) UpdateGroupsForUser(ctx context.Context, userID string, groupIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.UpdateGroupsForUser")
	defer span.End()

	if len(groupIDs) == 0 {
		_, err := s.db.Statement(ctx).
			Delete("group_members").
			Where(sq.Eq{"user_id": userID}).
			ExecContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to remove all group memberships: %v", err)
		}
		return nil
	}

	now := time.Now().UTC()

	insert := s.db.Statement(ctx).
		Insert("group_members").
		Columns("group_id", "user_id", "tenant_id", "role", "created_at", "updated_at").
		Suffix("ON CONFLICT (group_id, user_id) DO UPDATE SET updated_at = EXCLUDED.updated_at")

	for _, groupID := range groupIDs {
		insert = insert.Values(groupID, userID, "default", "member", now, now)
	}

	if _, err := insert.ExecContext(ctx); err != nil {
		if IsForeignKeyViolation(err) {
			return WrapForeignKeyError(err, "one or more groups do not exist")
		}
		return fmt.Errorf("failed to upsert group memberships: %v", err)
	}

	_, err := s.db.Statement(ctx).
		Delete("group_members").
		Where(sq.And{
			sq.Eq{"user_id": userID},
			sq.NotEq{"group_id": groupIDs},
		}).
		ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to remove old group memberships: %v", err)
	}

	return nil
}

// RemoveGroupsForUser removes all group memberships for a user and returns the list of removed group IDs.
func (s *Storage) RemoveGroupsForUser(ctx context.Context, userID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.RemoveGroupsForUser")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Delete("group_members").
		Where(sq.Eq{"user_id": userID}).
		Suffix("RETURNING group_id").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to remove group memberships: %v", err)
	}
	defer rows.Close()

	groupIDs := make([]string, 0)
	for rows.Next() {
		var groupID string
		if err := rows.Scan(&groupID); err != nil {
			return nil, fmt.Errorf("failed to scan group ID: %v", err)
		}
		groupIDs = append(groupIDs, groupID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating removed groups: %v", err)
	}

	return groupIDs, nil
}

// scanGroup scans a database row into a Group struct.
func scanGroup(row interface{ Scan(...interface{}) error }) (*types.Group, error) {
	group := &types.Group{}
	var typeStr string
	err := row.Scan(
		&group.ID,
		&group.Name,
		&group.TenantId,
		&group.Description,
		&typeStr,
		&group.CreatedAt,
		&group.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	group.Type, err = types.ParseGroupType(typeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid group type %q: %v", typeStr, err)
	}

	return group, nil
}
