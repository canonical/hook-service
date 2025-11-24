// Copyright 2025 Canonical Ltd
// SPDX-License-Identifier: AGPL-3.0

package groups

import "errors"

var (
	ErrGroupNotFound       = errors.New("group not found")
	ErrInvalidGroupName    = errors.New("invalid group name")
	ErrDuplicateGroup      = errors.New("group already exists")
	ErrInvalidGroupType    = errors.New("invalid group type")
	ErrInvalidTenant       = errors.New("invalid tenant")
	ErrInternalServerError = errors.New("internal server error")
)
