package groups

import "time"

type groupType string

const (
	GroupTypeExternal groupType = "external"
	GroupTypeLocal    groupType = "local"
)

// Group represents a group of users.
type Group struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Organization string    `json:"organization" default:"default"`
	Description  string    `json:"description"`
	Type         groupType `json:"type" default:"local"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type GroupUser struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Organization string `json:"organization" default:"default"`
}

func parseGroupType(s string) (groupType, error) {
	switch s {
	case "local", "":
		return GroupTypeLocal, nil
	case "external":
		return GroupTypeExternal, nil
	default:
		return "", NewInvalidGroupTypeError(s, "")
	}
}
