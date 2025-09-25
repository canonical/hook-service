// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authorization

const (
	CAN_ACCESS_RELATION = "can_access"
	MEMBER_RELATION     = "member"
	WILDCARD            = "*"
)

func UserTuple(userId string) string {
	return "user:" + userId
}

func ClientTuple(clientId string) string {
	return "client:" + clientId
}

func GroupTuple(groupId string) string {
	// Groups may include invalid characters for an ofga resource (e.g. spaces)
	// that's why b64 encode them
	// TODO: Once database support is implemented, we should consider using IDs
	// instead of encoded names
	return "group:" + groupId
}

func GroupMemberTuple(groupId string) string {
	return GroupTuple(groupId) + "#" + MEMBER_RELATION
}
