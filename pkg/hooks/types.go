// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package hooks

import "encoding/json"

// TokenHookRequest is the request body sent to the Ory Hydra token hook.
type TokenHookRequest struct {
	Session *Session `json:"session"`
	Request Request  `json:"request"`
}

// Request is the OAuth 2.0 request context within a TokenHookRequest.
type Request struct {
	ClientID        string              `json:"client_id"`
	RequestedScopes []string            `json:"requested_scopes"`
	GrantedScopes   []string            `json:"granted_scopes"`
	GrantedAudience []string            `json:"granted_audience"`
	GrantTypes      []string            `json:"grant_types"`
	Payload         map[string][]string `json:"payload"`
}

// Session represents the session data sent in the token hook request.
type Session struct {
	Extra          map[string]interface{} `json:"extra"`
	ClientID       string                 `json:"client_id"`
	DefaultSession *DefaultSession        `json:"id_token"`
}

// UnmarshalJSON implements json.Unmarshaler to handle both id_token and camelCase idToken.
func (s *Session) UnmarshalJSON(data []byte) error {
	type rawSession Session
	var raw struct {
		rawSession
		AltDefaultSession *DefaultSession `json:"idToken"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = Session(raw.rawSession)
	if s.DefaultSession == nil && raw.AltDefaultSession != nil {
		s.DefaultSession = raw.AltDefaultSession
	}
	return nil
}

// IDTokenClaims returns the claims container from DefaultSession, if present.
func (s *Session) IDTokenClaims() *IDTokenClaims {
	if s == nil || s.DefaultSession == nil {
		return nil
	}
	return s.DefaultSession.IDTokenClaims()
}

// DefaultSession represents OpenID Connect session data.
type DefaultSession struct {
	Subject string         `json:"subject"`
	Claims  *IDTokenClaims `json:"id_token_claims"`
}

// IDTokenClaims returns the claims pointer, initializing it if nil.
func (d *DefaultSession) IDTokenClaims() *IDTokenClaims {
	if d == nil {
		return nil
	}
	if d.Claims == nil {
		d.Claims = &IDTokenClaims{}
	}
	return d.Claims
}

// IDTokenClaims represents the claims inside an ID token.
type IDTokenClaims struct {
	Subject  string                 `json:"sub,omitempty"`
	Issuer   string                 `json:"iss,omitempty"`
	Audience []string               `json:"aud,omitempty"`
	JTI      string                 `json:"jti,omitempty"`
	Extra    map[string]interface{} `json:"ext,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler to handle both "ext" and "extra" keys.
func (c *IDTokenClaims) UnmarshalJSON(data []byte) error {
	type rawClaims IDTokenClaims
	var raw struct {
		rawClaims
		AltExtra map[string]interface{} `json:"extra"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = IDTokenClaims(raw.rawClaims)
	if c.Extra == nil && raw.AltExtra != nil {
		c.Extra = raw.AltExtra
	}
	return nil
}

// ToMap returns a map representation of the ID token claims for inclusion in token session.
func (c *IDTokenClaims) ToMap() map[string]interface{} {
	if c == nil {
		return nil
	}
	ret := make(map[string]interface{}, len(c.Extra)+4)
	for k, v := range c.Extra {
		ret[k] = v
	}
	if c.Subject != "" {
		ret["sub"] = c.Subject
	}
	if c.Issuer != "" {
		ret["iss"] = c.Issuer
	}
	if c.JTI != "" {
		ret["jti"] = c.JTI
	}
	if len(c.Audience) > 0 {
		ret["aud"] = c.Audience
	}
	return ret
}

// TokenHookResponse is the response body returned to the Ory Hydra token hook.
type TokenHookResponse struct {
	Session ConsentRequestSessionData `json:"session"`
}

// ConsentRequestSessionData contains the enriched session claims for access and ID tokens.
type ConsentRequestSessionData struct {
	AccessToken map[string]interface{} `json:"access_token"`
	IDToken     map[string]interface{} `json:"id_token"`
}
