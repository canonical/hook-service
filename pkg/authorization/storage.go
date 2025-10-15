package authorization

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

var NO_MAPPING_FOR_GROUP = fmt.Errorf("no mapping for group")
var APP_ALREADY_EXISTS_IN_GROUP = fmt.Errorf("app already exists in group")
var APP_DOES_NOT_EXIST_IN_GROUP = fmt.Errorf("app does not exist in group")
var APP_DOES_NOT_EXIST = fmt.Errorf("app does not exist")

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
		return APP_ALREADY_EXISTS_IN_GROUP
	}

	group.Apps[app] = true
	if _, ok := s.AppToGroups[app]; !ok {
		s.AppToGroups[app] = make(map[string]bool)
	}
	s.AppToGroups[app][groupID] = true
	return nil
}

func (s *Storage) RemoveAllowedApps(ctx context.Context, groupID string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		return NO_MAPPING_FOR_GROUP
	}

	// Remove all apps associated with the group
	for app := range group.Apps {
		if groups, ok := s.AppToGroups[app]; ok {
			delete(groups, groupID)
			if len(groups) == 0 {
				delete(s.AppToGroups, app)
			}
		}
	}

	// Finally, remove the group itself
	delete(s.Groups, groupID)
	return nil
}

func (s *Storage) RemoveAllowedApp(ctx context.Context, groupID string, app string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		return NO_MAPPING_FOR_GROUP
	}

	// Remove app from group's Apps slice
	if _, ok := group.Apps[app]; ok {
		delete(group.Apps, app)
		if len(group.Apps) == 0 {
			delete(s.Groups, groupID)
		}
	} else {
		return APP_DOES_NOT_EXIST_IN_GROUP
	}

	// Also remove from AppToGroups mapping
	if groups, ok := s.AppToGroups[app]; ok {
		delete(groups, groupID)
		if len(groups) == 0 {
			delete(s.AppToGroups, app)
		}
	} else {
		return APP_DOES_NOT_EXIST_IN_GROUP
	}

	return nil
}

func (s *Storage) RemoveAllowedGroupsForApp(ctx context.Context, app string) error {
	groups, exists := s.AppToGroups[app]
	if !exists {
		return APP_DOES_NOT_EXIST
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
	return nil
}

func (s *Storage) GetAllowedGroupsForApp(ctx context.Context, app string) ([]string, error) {
	groupsMap, exists := s.AppToGroups[app]
	if !exists {
		return nil, APP_DOES_NOT_EXIST
	}

	var groups []string
	for groupID := range groupsMap {
		groups = append(groups, groupID)
	}
	return groups, nil
}
