// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"
)

func TestMechanismName(t *testing.T) {
	m := &Mechanism{}
	if got := m.Name(); got != "OAUTHBEARER" {
		t.Errorf("m.Name() = %q, want %q", got, "OAUTHBEARER")
	}
}

func TestMechanismStart(t *testing.T) {
	tests := []struct {
		name        string
		provider    TokenProvider
		wantErr     bool
		errContains string
		wantIR      []byte
	}{
		{
			name:        "nil token provider fails",
			provider:    nil,
			wantErr:     true,
			errContains: "token provider is required",
		},
		{
			name: "token provider error propagated",
			provider: TokenFunc(func(ctx context.Context) (string, error) {
				return "", errors.New("upstream hydra down")
			}),
			wantErr:     true,
			errContains: "upstream hydra down",
		},
		{
			name: "empty token returned fails",
			provider: TokenFunc(func(ctx context.Context) (string, error) {
				return "", nil
			}),
			wantErr:     true,
			errContains: "obtained empty oauth token",
		},
		{
			name: "valid token produces RFC 7628 GS2 framing",
			provider: TokenFunc(func(ctx context.Context) (string, error) {
				return "jwt-access-token-xyz", nil
			}),
			wantErr: false,
			wantIR:  []byte("n,,\x01auth=Bearer jwt-access-token-xyz\x01\x01"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			m := &Mechanism{TokenProvider: tt.provider}

			sm, ir, err := m.Start(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("m.Start() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("m.Start() error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}

			if sm == nil {
				t.Fatal("m.Start() returned nil StateMachine")
			}
			if !bytes.Equal(ir, tt.wantIR) {
				t.Errorf("m.Start() ir = %q, want %q", ir, tt.wantIR)
			}
		})
	}
}

func TestStateMachineNext(t *testing.T) {
	tests := []struct {
		name        string
		challenge   []byte
		wantDone    bool
		wantErr     bool
		errContains string
	}{
		{
			name:      "empty challenge indicates authentication success",
			challenge: []byte{},
			wantDone:  true,
			wantErr:   false,
		},
		{
			name:      "nil challenge indicates authentication success",
			challenge: nil,
			wantDone:  true,
			wantErr:   false,
		},
		{
			name:        "JSON error challenge parsed with status",
			challenge:   []byte(`{"status":"invalid_token","scope":"kafka"}`),
			wantDone:    false,
			wantErr:     true,
			errContains: "invalid_token",
		},
		{
			name:        "raw status error challenge parsed",
			challenge:   []byte("status=expired_token"),
			wantDone:    false,
			wantErr:     true,
			errContains: "expired_token",
		},
		{
			name:        "arbitrary error challenge returned",
			challenge:   []byte("unrecognized broker rejection"),
			wantDone:    false,
			wantErr:     true,
			errContains: "unrecognized broker rejection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			sm := &StateMachine{}

			done, resp, err := sm.Next(ctx, tt.challenge)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sm.Next() error = %v, wantErr %v", err, tt.wantErr)
			}
			if done != tt.wantDone {
				t.Errorf("sm.Next() done = %v, want %v", done, tt.wantDone)
			}
			if resp != nil {
				t.Errorf("sm.Next() resp = %v, want nil", resp)
			}
			if tt.wantErr && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("sm.Next() error = %q, want containing %q", err.Error(), tt.errContains)
			}

			// Verify idempotency once done
			if done {
				done2, _, err2 := sm.Next(ctx, []byte("ignored"))
				if err2 != nil || !done2 {
					t.Errorf("subsequent sm.Next() when done = (%v, %v), want (true, nil)", done2, err2)
				}
			}
		})
	}
}

func TestStateMachineNext_InvalidatesTokenProviderOnErrorChallenge(t *testing.T) {
	ctx := t.Context()

	t.Run("non-empty challenge invalidates token provider", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		provider := NewMockTokenProvider(ctrl)
		provider.EXPECT().Token(gomock.Any()).Return("valid-jwt", nil)
		provider.EXPECT().Invalidate()

		m := NewMechanism(provider)

		sm, _, err := m.Start(ctx)
		if err != nil {
			t.Fatalf("m.Start() unexpected error: %v", err)
		}

		done, _, nextErr := sm.Next(ctx, []byte(`{"status":"invalid_token"}`))
		if nextErr == nil {
			t.Fatal("expected error on error challenge, got nil")
		}
		if done {
			t.Error("expected done = false on error challenge")
		}
	})

	t.Run("successful authentication does not invalidate token provider", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		provider := NewMockTokenProvider(ctrl)
		provider.EXPECT().Token(gomock.Any()).Return("valid-jwt", nil)

		m := NewMechanism(provider)

		sm, _, err := m.Start(ctx)
		if err != nil {
			t.Fatalf("m.Start() unexpected error: %v", err)
		}

		done, _, nextErr := sm.Next(ctx, nil)
		if nextErr != nil {
			t.Fatalf("sm.Next() unexpected error: %v", nextErr)
		}
		if !done {
			t.Error("expected done = true on successful auth")
		}
	})
}

