package authorization

import (
	"context"
	"maps"
	"slices"
)

var _ (AuthorizationDatabaseInterface) = (*Storage)(nil)

type Group struct {
	ID   string
	Apps map[string]bool
}

type Storage struct {
	Groups      map[string]*Group
	AppToGroups map[string]map[string]bool
}

func NewStorage() *Storage {
	return &Storage{
		Groups:      make(map[string]*Group),
		AppToGroups: make(map[string]map[string]bool),
	}
}

func (s *Storage) GetAllowedApps(ctx context.Context, groupID string) ([]string, error) {
	group, exists := s.Groups[groupID]
	if !exists {
		return []string{}, nil
	}
	return slices.Collect(maps.Keys(group.Apps)), nil
}

func (s *Storage) AddAllowedApp(ctx context.Context, groupID string, app string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		group = &Group{
			ID:   groupID,
			Apps: make(map[string]bool),
		}
		s.Groups[groupID] = group
	}

	// Check if app already exists
	if _, ok := s.Groups[groupID].Apps[app]; ok {
		return errAppAlreadyExistsInGroup
	}

	group.Apps[app] = true
	if _, ok := s.AppToGroups[app]; !ok {
		s.AppToGroups[app] = make(map[string]bool)
	}
	s.AppToGroups[app][groupID] = true
	return nil
}

func (s *Storage) AddAllowedApps(ctx context.Context, groupID string, apps []string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		group = &Group{
			ID:   groupID,
			Apps: make(map[string]bool),
		}
		s.Groups[groupID] = group
	}

	for _, app := range apps {
		group.Apps[app] = true
		if _, ok := s.AppToGroups[app]; !ok {
			s.AppToGroups[app] = make(map[string]bool)
		}
		s.AppToGroups[app][groupID] = true
	}
	return nil
}

func (s *Storage) RemoveAllowedApps(ctx context.Context, groupID string) ([]string, error) {
	group, exists := s.Groups[groupID]
	if !exists {
		return nil, errGroupNotFound
	}

	apps := make([]string, len(group.Apps))
	// Remove all apps associated with the group
	for app := range group.Apps {
		apps = append(apps, app)
		if groups, ok := s.AppToGroups[app]; ok {
			delete(groups, groupID)
			if len(groups) == 0 {
				delete(s.AppToGroups, app)
			}
		}
	}

	// Finally, remove the group itself
	delete(s.Groups, groupID)
	return apps, nil
}

func (s *Storage) RemoveAllowedApp(ctx context.Context, groupID string, app string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		return errGroupNotFound
	}

	// Remove app from group's Apps slice
	if _, ok := group.Apps[app]; ok {
		delete(group.Apps, app)
		if len(group.Apps) == 0 {
			delete(s.Groups, groupID)
		}
	} else {
		return errAppDoesNotExistInGroup
	}

	// Also remove from AppToGroups mapping
	if groups, ok := s.AppToGroups[app]; ok {
		delete(groups, groupID)
		if len(groups) == 0 {
			delete(s.AppToGroups, app)
		}
	} else {
		return errAppDoesNotExistInGroup
	}

	return nil
}

func (s *Storage) RemoveAllAllowedGroupsForApp(ctx context.Context, app string) ([]string, error) {
	groups, exists := s.AppToGroups[app]
	if !exists {
		return nil, errAppDoesNotExist
	}

	for groupID := range groups {
		if group, ok := s.Groups[groupID]; ok {
			// Remove app from group's Apps slice
			delete(group.Apps, app)
			if len(group.Apps) == 0 {
				delete(s.Groups, groupID)
			}
		}
	}

	delete(s.AppToGroups, app)
	return slices.Collect(maps.Keys(groups)), nil
}

func (s *Storage) GetAllowedGroupsForApp(ctx context.Context, app string) ([]string, error) {
	groupsMap, exists := s.AppToGroups[app]
	if !exists {
		return nil, errAppDoesNotExist
	}

	var groups []string
	for groupID := range groupsMap {
		groups = append(groups, groupID)
	}
	return groups, nil
}

func (s *Storage) AddAllowedGroupsForApp(ctx context.Context, app string, groups []string) error {
	if _, ok := s.AppToGroups[app]; !ok {
		s.AppToGroups[app] = make(map[string]bool)
	}

	for _, groupID := range groups {
		// Ensure group exists
		group, exists := s.Groups[groupID]
		if !exists {
			group = &Group{
				ID:   groupID,
				Apps: make(map[string]bool),
			}
			s.Groups[groupID] = group
		}

		// Add app to group (idempotent)
		group.Apps[app] = true

		// Add group to AppToGroups mapping (idempotent)
		s.AppToGroups[app][groupID] = true
	}

	return nil
}
