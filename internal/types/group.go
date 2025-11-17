// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package types

import (
	"errors"
	"time"
)

type GroupType string

const (
	GroupTypeExternal GroupType = "external"
	GroupTypeLocal    GroupType = "local"
)

var ErrInvalidGroupType = errors.New("invalid group type")

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
	Role     string `json:"role"`
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
		return "", ErrInvalidGroupType
	}
}
