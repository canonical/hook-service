package groups

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

var GROUP_DOES_NOT_EXIST = fmt.Errorf("group does not exist")
var USER_ALREADY_IN_GROUP = fmt.Errorf("user already in group")
var _ GroupDatabaseInterface = NewStorage()

type Group struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Organization string   `json:"organization"`
	Description  string   `json:"description,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	Users        []string `json:"users,omitempty"`
}

type User struct {
	UserID string `json:"user_id"`
}

type Storage struct {
	Groups     map[string]*Group
	GroupNames map[string]string
	Users      map[string]*User
}

func NewStorage() *Storage {
	return &Storage{
		Groups:     make(map[string]*Group),
		GroupNames: make(map[string]string),
		Users:      make(map[string]*User),
	}
}

func (s *Storage) ListGroups(ctx context.Context) ([]*Group, error) {
	return slices.Collect(maps.Values(s.Groups)), nil
}

func (s *Storage) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	group, exists := s.Groups[groupID]
	if !exists {
		return nil, GROUP_DOES_NOT_EXIST
	}

	return group, nil
}

func (s *Storage) GetGroupByName(ctx context.Context, name string) (*Group, error) {
	groupID, exists := s.GroupNames[name]
	if !exists {
		return nil, GROUP_DOES_NOT_EXIST
	}
	group, exists := s.Groups[groupID]
	if !exists {
		return nil, GROUP_DOES_NOT_EXIST
	}

	return group, nil
}

func (s *Storage) CreateGroup(ctx context.Context, name, organization string) (*Group, error) {
	group := NewGroup(name, organization)
	group.ID = strconv.Itoa(len(s.Groups) + 1)

	s.Groups[group.ID] = group
	s.GroupNames[group.Name] = group.ID

	return group, nil
}

func (s *Storage) DeleteGroup(ctx context.Context, groupID string) error {
	g, exists := s.Groups[groupID]
	if !exists {
		return GROUP_DOES_NOT_EXIST
	}
	delete(s.GroupNames, g.Name)
	delete(s.Groups, groupID)
	return nil
}

func (s *Storage) ListGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	group, exists := s.Groups[groupID]
	if !exists {
		return nil, GROUP_DOES_NOT_EXIST
	}
	return group.Users, nil
}

func (s *Storage) AddGroupMember(ctx context.Context, groupID string, userID string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		return GROUP_DOES_NOT_EXIST
	}
	if s.Users[userID] != nil {
		return USER_ALREADY_IN_GROUP
	}
	group.Users = append(group.Users, userID)
	s.Users[userID] = &User{UserID: userID}
	return nil
}

func (s *Storage) RemoveGroupMember(ctx context.Context, groupID string, userID string) error {
	group, exists := s.Groups[groupID]
	if !exists {
		return GROUP_DOES_NOT_EXIST
	}

	for i, user := range group.Users {
		if user == userID {
			group.Users = append(group.Users[:i], group.Users[i+1:]...)
			break
		}
	}
	return nil
}

func (s *Storage) ListUserGroups(ctx context.Context, userID string) ([]*Group, error) {
	var groups []*Group
	for _, group := range s.Groups {
		if slices.Contains(group.Users, userID) {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

func NewGroup(name, organization string) *Group {
	return &Group{
		ID:           "",
		Name:         name,
		Organization: organization,
	}
}

func NewUser(userID string) *User {
	return &User{
		UserID: userID,
	}
}
