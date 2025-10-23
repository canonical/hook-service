package groups

import "errors"

var (
	ErrGroupNotFound       = errors.New("group not found")
	ErrInvalidGroupName    = errors.New("invalid group name")
	ErrDuplicateGroup      = errors.New("group already exists")
	ErrInvalidGroupType    = errors.New("invalid group type")
	ErrInvalidOrganization = errors.New("invalid organization")
	ErrInternalServerError = errors.New("internal server error")
)
