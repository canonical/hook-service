// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type Middleware struct {
	config   *Config
	verifier TokenVerifierInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (m *Middleware) Authenticate() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := m.tracer.Start(r.Context(), "authentication.Middleware.Authenticate")
			defer span.End()

			token, found := m.getBearerToken(r.Header)
			if !found {
				m.unauthorizedResponse(w, "missing authorization header")
				return
			}

			idToken, err := m.verifier.VerifyToken(ctx, token)
			if err != nil {
				m.logger.Debugf("JWT verification failed: %v", err)
				m.unauthorizedResponse(w, "invalid token")
				return
			}

			if !m.isAuthorized(idToken) {
				m.unauthorizedResponse(w, "unauthorized")
				return
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (m *Middleware) getBearerToken(headers http.Header) (string, bool) {
	bearer := headers.Get("Authorization")
	if bearer == "" {
		return "", false
	}

	// Only support "Bearer <token>" format (RFC 6750)
	if !strings.HasPrefix(bearer, "Bearer ") {
		return "", false
	}

	return strings.TrimPrefix(bearer, "Bearer "), true
}

func (m *Middleware) isAuthorized(token *oidc.IDToken) bool {
	var claims struct {
		Subject string   `json:"sub"`
		Scope   string   `json:"scope"`
		Scopes  []string `json:"scp"`
	}

	if err := token.Claims(&claims); err != nil {
		m.logger.Debugf("Failed to extract claims: %v", err)
		return false
	}

	if len(m.config.AllowedSubjects) > 0 {
		for _, allowedSub := range m.config.AllowedSubjects {
			if claims.Subject == allowedSub {
				return true
			}
		}
	}

	if m.config.RequiredScope != "" {
		if claims.Scope != "" {
			scopes := strings.Fields(claims.Scope)
			for _, scope := range scopes {
				if scope == m.config.RequiredScope {
					return true
				}
			}
		}

		for _, scope := range claims.Scopes {
			if scope == m.config.RequiredScope {
				return true
			}
		}
	}

	if len(m.config.AllowedSubjects) == 0 && m.config.RequiredScope == "" {
		m.logger.Debugf("No authorization criteria configured")
		m.logger.Security().AuthzFailure(claims.Subject, "jwt_api_access")
		return false
	}

	m.logger.Security().AuthzFailure(claims.Subject, "jwt_api_access")
	return false
}

func (m *Middleware) unauthorizedResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  http.StatusUnauthorized,
		"message": message,
	}); err != nil {
		m.logger.Errorf("failed to encode unauthorized response: %v", err)
	}
}

func NewMiddleware(config *Config, verifier TokenVerifierInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Middleware {
	return &Middleware{
		config:   config,
		verifier: verifier,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
