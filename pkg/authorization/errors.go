// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

import "errors"

// Package-level sentinel errors for the authorization package.
// These are kept unexported because they are intended for internal
// package comparisons with errors.Is().
var (
	errGroupNotFound           = errors.New("group not found")
	errAppAlreadyExistsInGroup = errors.New("app already exists in group")
	errAppDoesNotExist         = errors.New("app does not exist")
	errInvalidGroupID          = errors.New("invalid group id")
)
