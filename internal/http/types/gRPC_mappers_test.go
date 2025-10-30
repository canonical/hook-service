// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package types

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	v0Types "github.com/canonical/identity-platform-api/v0/http"
	v0Roles "github.com/canonical/identity-platform-api/v0/roles"
	rpcStatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestForwardErrorResponseRewriter(t *testing.T) {
	untouchedResponse := &v0Roles.ListRolesResp{}

	tests := []struct {
		name     string
		response proto.Message
		expected any
	}{
		{
			name:     "Valid grpc status",
			response: &rpcStatus.Status{Code: int32(codes.NotFound), Message: "Resource not found"},
			expected: &v0Types.ErrorResponse{
				Status:  int32(http.StatusNotFound),
				Message: "Resource not found",
			},
		},
		{
			name:     "Invalid response type",
			response: untouchedResponse,
			expected: untouchedResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, _ := ForwardErrorResponseRewriter(context.Background(), test.response)

			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("expected result: %v, got: %v", test.expected, result)
			}
		})
	}
}
