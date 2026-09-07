// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"encoding/base64"
	"testing"
)

func TestExtractSubjectFromJWT(t *testing.T) {
	validHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user:alice@example.com","org":"canonical","iss":"sts"}`))
	emptySubPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"org":"canonical","iss":"sts"}`))
	invalidJSONPayload := base64.RawURLEncoding.EncodeToString([]byte(`{not valid json`))
	validSig := base64.RawURLEncoding.EncodeToString([]byte("fakesignaturebytes"))

	tests := []struct {
		name        string
		token       string
		expectedSub string
		expectErr   bool
	}{
		{
			name:        "valid token with sub claim",
			token:       validHeader + "." + validPayload + "." + validSig,
			expectedSub: "user:alice@example.com",
			expectErr:   false,
		},
		{
			name:        "token without 3 parts",
			token:       "invalid.token",
			expectedSub: "",
			expectErr:   true,
		},
		{
			name:        "token with invalid base64 payload",
			token:       validHeader + ".!@#$%." + validSig,
			expectedSub: "",
			expectErr:   true,
		},
		{
			name:        "token with invalid json payload",
			token:       validHeader + "." + invalidJSONPayload + "." + validSig,
			expectedSub: "",
			expectErr:   true,
		},
		{
			name:        "token with empty sub claim",
			token:       validHeader + "." + emptySubPayload + "." + validSig,
			expectedSub: "",
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, err := ExtractSubjectFromJWT(tt.token)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil (sub: %q)", sub)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if sub != tt.expectedSub {
					t.Fatalf("expected sub %q, got %q", tt.expectedSub, sub)
				}
			}
		})
	}
}

func TestExtractClaims(t *testing.T) {
	validHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user:bob@example.com","scope":"read write","scp":["admin"]}`))
	validSig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))

	token := validHeader + "." + validPayload + "." + validSig
	claims, err := ExtractClaims(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if claims.Subject != "user:bob@example.com" {
		t.Errorf("expected subject %q, got %q", "user:bob@example.com", claims.Subject)
	}
	if claims.Scope != "read write" {
		t.Errorf("expected scope %q, got %q", "read write", claims.Scope)
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != "admin" {
		t.Errorf("expected scopes [admin], got %v", claims.Scopes)
	}
}
