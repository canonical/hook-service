// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	kafkago_sasl "github.com/segmentio/kafka-go/sasl"
)

var (
	_ kafkago_sasl.Mechanism    = (*Mechanism)(nil)
	_ kafkago_sasl.StateMachine = (*StateMachine)(nil)
)

// MechanismName is the IANA-registered SASL mechanism name for OAuth 2.0 bearer tokens.
const MechanismName = "OAUTHBEARER"

// Mechanism implements the segmentio/kafka-go/sasl.Mechanism interface
// for SASL/OAUTHBEARER per RFC 7628.
type Mechanism struct {
	TokenProvider TokenProvider
}

// NewMechanism creates a new SASL/OAUTHBEARER Mechanism using the provided TokenProvider.
func NewMechanism(provider TokenProvider) *Mechanism {
	return &Mechanism{TokenProvider: provider}
}

// Name returns the SASL mechanism identifier.
func (m *Mechanism) Name() string {
	return MechanismName
}

// Start initiates SASL/OAUTHBEARER authentication by formatting the RFC 7628
// GS2 initial client response containing the bearer token.
func (m *Mechanism) Start(ctx context.Context) (kafkago_sasl.StateMachine, []byte, error) {
	if m.TokenProvider == nil {
		return nil, nil, errors.New("sasl oauthbearer token provider is required")
	}

	token, err := m.TokenProvider.Token(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to obtain oauth token for sasl authentication: %v", err)
	}
	if token == "" {
		return nil, nil, errors.New("obtained empty oauth token for sasl authentication")
	}

	// Format GS2 initial client response per RFC 7628 Section 3.1:
	// n,,\x01auth=Bearer <token>\x01\x01
	ir := fmt.Appendf(nil, "n,,\x01auth=Bearer %s\x01\x01", token)
	return &StateMachine{
		provider: m.TokenProvider,
	}, ir, nil
}

// StateMachine implements the segmentio/kafka-go/sasl.StateMachine interface
// for the SASL/OAUTHBEARER challenge-response flow.
type StateMachine struct {
	provider TokenProvider
	done     bool
}

// Next handles broker challenges during SASL negotiation. An empty challenge indicates
// successful authentication. A non-empty challenge indicates an RFC 7628 error challenge.
func (s *StateMachine) Next(ctx context.Context, challenge []byte) (bool, []byte, error) {
	if s.done {
		return true, nil, nil
	}

	if len(challenge) == 0 {
		s.done = true
		return true, nil, nil
	}

	if s.provider != nil {
		s.provider.Invalidate()
	}

	// Non-empty challenge from Kafka server indicates authentication failure.
	return false, nil, ParseErrorChallenge(challenge)
}

// ParseErrorChallenge parses an RFC 7628 SASL error challenge payload from the broker.
func ParseErrorChallenge(challenge []byte) error {
	if len(challenge) == 0 {
		return nil
	}

	trimmed := bytes.TrimSpace(challenge)

	// Extract "status" from JSON error payload: {"status":"invalid_token",...}
	if _, after, found := bytes.Cut(trimmed, []byte(`"status":`)); found {
		s := strings.TrimSpace(string(after))
		if val, ok := strings.CutPrefix(s, `"`); ok {
			if status, _, cutOk := strings.Cut(val, `"`); cutOk {
				return fmt.Errorf("sasl oauthbearer authentication failed: %s (challenge: %s)", status, string(trimmed))
			}
		}
	}

	// Fallback to checking for raw "status=" prefix
	str := string(trimmed)
	if val, ok := strings.CutPrefix(str, "status="); ok {
		return fmt.Errorf("sasl oauthbearer authentication failed: %s", val)
	}

	return fmt.Errorf("sasl oauthbearer authentication failed: %s", str)
}
