// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package groups

import (
	"context"
)

type contextKey string

const userIDContextKey contextKey = "user_id"

// ContextWithUserID returns a copy of parent context with the user ID set.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

// UserIDFromContext extracts the user ID from the context.
func UserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(userIDContextKey).(string); ok && v != "" {
		return v
	}
	return ""
}
