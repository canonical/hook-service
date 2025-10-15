// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import "errors"

var (
	ErrGroupNotFound           = errors.New("group not found")
	ErrAppAlreadyExistsInGroup = errors.New("app already exists in group")
	ErrAppDoesNotExistInGroup  = errors.New("app does not exist in group")
	ErrAppDoesNotExist         = errors.New("app does not exist")
	ErrInternalServerError     = errors.New("internal server error")
)
