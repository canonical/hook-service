package groups

import (
	"fmt"
)

// Error codes for group-related errors
const (
	ErrCodeGroupNotFound      = "GROUP_NOT_FOUND"
	ErrCodeInvalidGroupName   = "INVALID_GROUP_NAME"
	ErrCodeInvalidGroupType   = "INVALID_GROUP_TYPE"
	ErrCodeDuplicateGroup     = "DUPLICATE_GROUP"
	ErrCodeUserNotFound       = "USER_NOT_FOUND"
	ErrCodeUserAlreadyInGroup = "USER_ALREADY_IN_GROUP"
	ErrCodeUserNotInGroup     = "USER_NOT_IN_GROUP"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeValidationError    = "VALIDATION_ERROR"
)

// GroupError represents a domain-specific error for group operations
type GroupError struct {
	Code       string            // Machine-readable error code
	Message    string            // Human-readable error message
	Op         string            // Operation that failed (e.g., "CreateGroup", "DeleteGroup")
	Metadata   map[string]string // Additional context about the error
	Underlying error             // The underlying error if any
}

// Error implements the error interface
func (e *GroupError) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("%s: %s", e.Op, e.Message)
	}
	return e.Message
}

// Is implements error unwrapping for errors.Is
func (e *GroupError) Is(target error) bool {
	t, ok := target.(*GroupError)
	if !ok {
		return false
	}
	return t.Code == e.Code
}

// Constructor functions for common errors
func NewGroupNotFoundError(groupID string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeGroupNotFound,
		Message: "group not found",
		Op:      op,
		Metadata: map[string]string{
			"group_id": groupID,
		},
	}
}

func NewInvalidGroupNameError(name string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeInvalidGroupName,
		Message: "invalid group name",
		Op:      op,
		Metadata: map[string]string{
			"group_name": name,
		},
	}
}

func NewInvalidGroupTypeError(groupType string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeInvalidGroupType,
		Message: "invalid group type",
		Op:      op,
		Metadata: map[string]string{
			"group_type": groupType,
		},
	}
}

func NewDuplicateGroupError(name string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeDuplicateGroup,
		Message: "group already exists",
		Op:      op,
		Metadata: map[string]string{
			"group_name": name,
		},
	}
}

func NewUserNotFoundError(userID string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeUserNotFound,
		Message: "user not found",
		Op:      op,
		Metadata: map[string]string{
			"user_id": userID,
		},
	}
}

func NewUserAlreadyInGroupError(userID, groupID string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeUserAlreadyInGroup,
		Message: "user is already a member of the group",
		Op:      op,
		Metadata: map[string]string{
			"user_id":  userID,
			"group_id": groupID,
		},
	}
}

func NewUserNotInGroupError(userID, groupID string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeUserNotInGroup,
		Message: "user is not a member of the group",
		Op:      op,
		Metadata: map[string]string{
			"user_id":  userID,
			"group_id": groupID,
		},
	}
}

func NewValidationError(field, reason string, op string) *GroupError {
	return &GroupError{
		Code:    ErrCodeValidationError,
		Message: "validation failed",
		Op:      op,
		Metadata: map[string]string{
			"field":  field,
			"reason": reason,
		},
	}
}
