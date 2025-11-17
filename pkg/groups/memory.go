// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import (
	"context"
	"sync"
	"time"

	"github.com/canonical/hook-service/internal/types"
	"github.com/google/uuid"
)

var _ DatabaseInterface = (*Storage)(nil)

type Storage struct {
	mu     sync.RWMutex
	groups map[string]*types.Group
	users  map[string][]string
}

func (m *Storage) ListGroups(ctx context.Context) ([]*types.Group, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	groups := make([]*types.Group, 0, len(m.groups))
	for _, group := range m.groups {
		groups = append(groups, group)
	}
	return groups, nil
}

func (m *Storage) CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if group.ID == "" {
		group.ID = uuid.New().String()
	}
	group.CreatedAt = time.Now()
	group.UpdatedAt = time.Now()

	m.groups[group.ID] = group
	return group, nil
}

func (m *Storage) GetGroup(ctx context.Context, id string) (*types.Group, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	group, ok := m.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func (m *Storage) UpdateGroup(ctx context.Context, id string, group *types.Group) (*types.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existingGroup, ok := m.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}

	// Update fields
	existingGroup.TenantId = group.TenantId
	existingGroup.Description = group.Description
	existingGroup.Type = group.Type
	existingGroup.UpdatedAt = time.Now()

	m.groups[id] = existingGroup

	return existingGroup, nil
}

func (m *Storage) DeleteGroup(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groups[id]; !ok {
		return ErrGroupNotFound
	}

	delete(m.groups, id)
	delete(m.users, id)
	return nil
}

func (m *Storage) AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groups[groupID]; !ok {
		return ErrGroupNotFound
	}

	userSet := make(map[string]struct{})
	for _, u := range m.users[groupID] {
		userSet[u] = struct{}{}
	}

	for _, userID := range userIDs {
		if _, ok := userSet[userID]; !ok {
			m.users[groupID] = append(m.users[groupID], userID)
			userSet[userID] = struct{}{}
		}
	}
	return nil
}

func (m *Storage) ListUsersInGroup(ctx context.Context, groupID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.groups[groupID]; !ok {
		return nil, ErrGroupNotFound
	}

	return m.users[groupID], nil
}

func (m *Storage) RemoveUsersFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groups[groupID]; !ok {
		return ErrGroupNotFound
	}

	usersToRemove := make(map[string]struct{})
	for _, u := range userIDs {
		usersToRemove[u] = struct{}{}
	}

	var newUsers []string
	for _, u := range m.users[groupID] {
		if _, found := usersToRemove[u]; !found {
			newUsers = append(newUsers, u)
		}
	}
	m.users[groupID] = newUsers
	return nil
}

func (m *Storage) RemoveAllUsersFromGroup(ctx context.Context, groupID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.groups[groupID]; !ok {
		return nil, ErrGroupNotFound
	}

	users := m.users[groupID]
	delete(m.users, groupID)
	return users, nil
}

func (m *Storage) GetGroupsForUser(ctx context.Context, userID string) ([]*types.Group, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[userID]
	if !ok {
		return nil, nil
	}

	var groups []*types.Group
	for _, groupID := range user {
		if group, ok := m.groups[groupID]; ok {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

func (m *Storage) UpdateGroupsForUser(ctx context.Context, userID string, groupIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, g := range groupIDs {
		if _, ok := m.groups[g]; !ok {
			return ErrGroupNotFound
		}
	}

	m.users[userID] = groupIDs
	return nil
}

func (m *Storage) RemoveGroupsForUser(ctx context.Context, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var removedFrom []string
	for groupID, users := range m.users {
		var newUsers []string
		removed := false
		for _, u := range users {
			if u == userID {
				removed = true
			} else {
				newUsers = append(newUsers, u)
			}
		}
		if removed {
			m.users[groupID] = newUsers
			removedFrom = append(removedFrom, groupID)
		}
	}
	return removedFrom, nil
}

func NewStorage() *Storage {
	return &Storage{
		groups: make(map[string]*types.Group),
		users:  make(map[string][]string),
	}
}
