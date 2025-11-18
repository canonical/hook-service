// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
)

var _ ClientInterface = (*StorageHookGroupsClient)(nil)

type StorageHookGroupsClient struct {
	db DatabaseInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// FetchUserGroups retrieves user groups from the local storage database.
func (c *StorageHookGroupsClient) FetchUserGroups(ctx context.Context, user User) ([]*types.Group, error) {
	ctx, span := c.tracer.Start(ctx, "hooks.StorageHookGroupsClient.FetchUserGroups")
	defer span.End()

	userId := user.Email
	if user.ClientId != "" {
		userId = user.ClientId
	}
	if userId == "" {
		c.logger.Warnf("User ID is empty for user: %#v", user)
		return nil, nil
	}

	groups, err := c.db.GetGroupsForUser(ctx, userId)
	if err != nil {
		return nil, err
	}
	return groups, nil
}

// NewLocalStorageClient creates a new StorageHookGroupsClient.
func NewLocalStorageClient(db DatabaseInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *StorageHookGroupsClient {
	s := new(StorageHookGroupsClient)
	s.db = db
	s.tracer = tracer
	s.monitor = monitor
	s.logger = logger
	return s
}
