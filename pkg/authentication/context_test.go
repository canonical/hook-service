// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"context"
	"testing"
)

func TestUserIDFromContext(t *testing.T) {
	tests := []struct {
		name           string
		ctx            context.Context
		expectedUserID string
	}{
		{
			name:           "nil context returns empty",
			ctx:            nil,
			expectedUserID: "",
		},
		{
			name:           "empty context returns empty",
			ctx:            context.Background(),
			expectedUserID: "",
		},
		{
			name:           "context with user ID value returns user ID",
			ctx:            ContextWithUserID(context.Background(), "user-123"),
			expectedUserID: "user-123",
		},
		{
			name:           "context with empty user ID value returns empty",
			ctx:            ContextWithUserID(context.Background(), ""),
			expectedUserID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := UserIDFromContext(tt.ctx)
			if userID != tt.expectedUserID {
				t.Fatalf("expected user ID %q, got %q", tt.expectedUserID, userID)
			}
		})
	}
}
