// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package storage

import (
	"context"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// GetAllowedApps retrieves all application IDs allowed for a specific group.
func (s *Storage) GetAllowedApps(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.GetAllowedApps")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Select("application_id").
		From("application_groups").
		Where(sq.Eq{"group_id": groupID}).
		OrderBy("application_id ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query allowed apps: %v", err)
	}
	defer rows.Close()

	appIDs := make([]string, 0)
	for rows.Next() {
		var appID string
		if err := rows.Scan(&appID); err != nil {
			return nil, fmt.Errorf("failed to scan application ID: %v", err)
		}
		appIDs = append(appIDs, appID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating allowed apps: %v", err)
	}

	return appIDs, nil
}

// AddAllowedApp adds a single application to the allowed list for a group.
func (s *Storage) AddAllowedApp(ctx context.Context, groupID string, appID string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.AddAllowedApp")
	defer span.End()

	now := time.Now().UTC()

	_, err := s.db.Statement(ctx).
		Insert("application_groups").
		Columns("group_id", "application_id", "tenant_id", "created_at", "updated_at").
		Values(groupID, appID, "default", now, now).
		ExecContext(ctx)
	if err != nil {
		if IsDuplicateKeyError(err) {
			return WrapDuplicateKeyError(err, "app already allowed for group")
		}
		if IsForeignKeyViolation(err) {
			return WrapForeignKeyError(err, "group does not exist")
		}
		return fmt.Errorf("failed to insert allowed app: %v", err)
	}

	return nil
}

// AddAllowedApps adds multiple applications to the allowed list for a group.
func (s *Storage) AddAllowedApps(ctx context.Context, groupID string, appIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.AddAllowedApps")
	defer span.End()

	if len(appIDs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	insert := s.db.Statement(ctx).
		Insert("application_groups").
		Columns("group_id", "application_id", "tenant_id", "created_at", "updated_at").
		Suffix("ON CONFLICT DO NOTHING")

	for _, appID := range appIDs {
		insert = insert.Values(groupID, appID, "default", now, now)
	}

	_, err := insert.ExecContext(ctx)
	if err != nil {
		if IsForeignKeyViolation(err) {
			return WrapForeignKeyError(err, "group does not exist")
		}
		return fmt.Errorf("failed to insert allowed apps: %v", err)
	}

	return nil
}

// RemoveAllowedApp removes a single application from the allowed list for a group.
func (s *Storage) RemoveAllowedApp(ctx context.Context, groupID string, appID string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.RemoveAllowedApp")
	defer span.End()

	_, err := s.db.Statement(ctx).
		Delete("application_groups").
		Where(sq.Eq{"group_id": groupID, "application_id": appID}).
		ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to remove allowed app: %v", err)
	}

	return nil
}

// RemoveAllowedApps removes all applications from the allowed list for a group and returns the removed app IDs.
func (s *Storage) RemoveAllowedApps(ctx context.Context, groupID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.RemoveAllowedApps")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Delete("application_groups").
		Where(sq.Eq{"group_id": groupID}).
		Suffix("RETURNING application_id").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to remove allowed apps: %v", err)
	}
	defer rows.Close()

	appIDs := make([]string, 0)
	for rows.Next() {
		var appID string
		if err := rows.Scan(&appID); err != nil {
			return nil, fmt.Errorf("failed to scan application ID: %v", err)
		}
		appIDs = append(appIDs, appID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating removed apps: %v", err)
	}

	return appIDs, nil
}

// AddAllowedGroupsForApp adds multiple groups to the allowed list for an application.
func (s *Storage) AddAllowedGroupsForApp(ctx context.Context, appID string, groupIDs []string) error {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.AddAllowedGroupsForApp")
	defer span.End()

	if len(groupIDs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	insert := s.db.Statement(ctx).
		Insert("application_groups").
		Columns("group_id", "application_id", "tenant_id", "created_at", "updated_at").
		Suffix("ON CONFLICT DO NOTHING")

	for _, groupID := range groupIDs {
		insert = insert.Values(groupID, appID, "default", now, now)
	}

	_, err := insert.ExecContext(ctx)
	if err != nil {
		if IsForeignKeyViolation(err) {
			return WrapForeignKeyError(err, "one or more groups do not exist")
		}
		return fmt.Errorf("failed to insert allowed groups for app: %v", err)
	}

	return nil
}

// GetAllowedGroupsForApp retrieves all group IDs that are allowed to access a specific application.
func (s *Storage) GetAllowedGroupsForApp(ctx context.Context, appID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.GetAllowedGroupsForApp")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Select("group_id").
		From("application_groups").
		Where(sq.Eq{"application_id": appID}).
		OrderBy("group_id ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query allowed groups for app: %v", err)
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
		return nil, fmt.Errorf("error iterating allowed groups: %v", err)
	}

	return groupIDs, nil
}

// RemoveAllAllowedGroupsForApp removes all groups from the allowed list for an application and returns the removed group IDs.
func (s *Storage) RemoveAllAllowedGroupsForApp(ctx context.Context, appID string) ([]string, error) {
	ctx, span := s.tracer.Start(ctx, "storage.Storage.RemoveAllAllowedGroupsForApp")
	defer span.End()

	rows, err := s.db.Statement(ctx).
		Delete("application_groups").
		Where(sq.Eq{"application_id": appID}).
		Suffix("RETURNING group_id").
		QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to remove allowed groups for app: %v", err)
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
