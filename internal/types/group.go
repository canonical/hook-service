// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package types

import (
	"encoding/json"
	"errors"
	"time"
)

type GroupType int

const (
	GroupTypeLocal    GroupType = 0
	GroupTypeExternal GroupType = 1
)

type Role int

const (
	RoleMember Role = 0
	RoleOwner  Role = 1
)

var (
	ErrInvalidGroupType = errors.New("invalid group type")
	ErrInvalidRole      = errors.New("invalid role")
)

// Group represents a group of users.
type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	TenantId    string    `json:"tenant" default:"default"`
	Description string    `json:"description"`
	Type        GroupType `json:"type" default:"local"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GroupUser represents a user's membership in a group.
type GroupUser struct {
	ID       string `json:"id"`
	Role     Role   `json:"role"`
	TenantId string `json:"tenant" default:"default"`
}

// ParseGroupType converts a string to a GroupType.
func ParseGroupType(s string) (GroupType, error) {
	switch s {
	case "local", "":
		return GroupTypeLocal, nil
	case "external":
		return GroupTypeExternal, nil
	default:
		return GroupTypeLocal, ErrInvalidGroupType
	}
}

func (t GroupType) String() string {
	switch t {
	case GroupTypeLocal:
		return "local"
	case GroupTypeExternal:
		return "external"
	default:
		return "unknown"
	}
}

// MarshalJSON marshals the enum as a quoted json string
func (t GroupType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// UnmarshalJSON unmarshals a quoted json string to the enum value
func (t *GroupType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	val, err := ParseGroupType(s)
	if err != nil {
		return err
	}
	*t = val
	return nil
}

// ParseRole converts a string to a Role.
func ParseRole(s string) (Role, error) {
	switch s {
	case "member", "":
		return RoleMember, nil
	case "owner":
		return RoleOwner, nil
	default:
		return RoleMember, ErrInvalidRole
	}
}

func (r Role) String() string {
	switch r {
	case RoleMember:
		return "member"
	case RoleOwner:
		return "owner"
	default:
		return "unknown"
	}
}

// MarshalJSON marshals the enum as a quoted json string
func (r Role) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

// UnmarshalJSON unmarshals a quoted json string to the enum value
func (r *Role) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	val, err := ParseRole(s)
	if err != nil {
		return err
	}
	*r = val
	return nil
}
