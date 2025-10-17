package authorization

import "errors"

// Package-level sentinel errors for the authorization package.
// These are kept unexported because they are intended for internal
// package comparisons with errors.Is().
var (
	errGroupNotFound           = errors.New("group not found")
	errAppAlreadyExistsInGroup = errors.New("app already exists in group")
	errAppDoesNotExistInGroup  = errors.New("app does not exist in group")
	errAppDoesNotExist         = errors.New("app does not exist")
)
