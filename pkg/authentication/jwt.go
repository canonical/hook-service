// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ExtractClaims decodes and parses claims from an unverified JWT token payload.
// Note: This does not verify the cryptographic signature; callers must ensure
// the token was validated beforehand (e.g. via Istio/Cerberus or VerifyToken).
func ExtractClaims(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadBytes, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("failed to decode jwt payload: %v", err)
		}
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal jwt claims: %v", err)
	}

	return &claims, nil
}

// ExtractSubjectFromJWT decodes the subject from an unverified JWT token payload.
// Note: This does not verify the cryptographic signature; callers must ensure
// the token was validated beforehand (e.g. via Istio/Cerberus or VerifyToken).
func ExtractSubjectFromJWT(token string) (string, error) {
	claims, err := ExtractClaims(token)
	if err != nil {
		return "", err
	}

	if claims.Subject == "" {
		return "", errors.New("subject claim is empty")
	}

	return claims.Subject, nil
}
