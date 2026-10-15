// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"

	v1 "github.com/canonical/authorization-service/api/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// TestParseBrokers tests broker string splitting, trimming, and default fallback.
func TestParseBrokers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single broker",
			input:    "localhost:9092",
			expected: []string{"localhost:9092"},
		},
		{
			name:     "multiple brokers with spaces and trailing comma",
			input:    " localhost:9092 , broker2:9092 ,  ",
			expected: []string{"localhost:9092", "broker2:9092"},
		},
		{
			name:     "empty string defaults to localhost:9092",
			input:    "",
			expected: []string{"localhost:9092"},
		},
		{
			name:     "only commas and spaces defaults to localhost:9092",
			input:    " , ,   ",
			expected: []string{"localhost:9092"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseBrokers(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected len %d, got %d (%v)", len(tc.expected), len(got), got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

// TestFormatEnvelope tests unmarshaling and JSON formatting of PermissionUpdateEnvelope payloads.
func TestFormatEnvelope(t *testing.T) {
	marshaller := protojson.MarshalOptions{
		Indent:          "  ",
		EmitUnpopulated: false,
	}

	t.Run("valid envelope", func(t *testing.T) {
		env := &v1.PermissionUpdateEnvelope{
			Version:        "1.0",
			Service:        "hook-service",
			MessageId:      "msg-123",
			IdempotencyKey: "idem-456",
			Operations: []*v1.PermissionOperation{
				{
					Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
					Subject:  "user:u1",
					Relation: "member",
					Object:   "group:admins",
				},
			},
		}

		raw, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("failed to marshal proto: %v", err)
		}

		jsonStr, err := formatEnvelope(raw, marshaller)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if !strings.Contains(jsonStr, `"service"`) || !strings.Contains(jsonStr, `"hook-service"`) {
			t.Errorf("expected json to contain service name, got: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"messageId"`) || !strings.Contains(jsonStr, `"msg-123"`) {
			t.Errorf("expected json to contain messageId, got: %s", jsonStr)
		}
	})

	t.Run("invalid payload", func(t *testing.T) {
		invalidBytes := []byte{0xff, 0xff, 0xff}
		_, err := formatEnvelope(invalidBytes, marshaller)
		if err == nil {
			t.Fatal("expected error unmarshaling invalid bytes, got nil")
		}
	})
}
