// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

// UserContextMiddleware extracts the caller's user identity from the Authorization Bearer JWT
// and injects it into the request context.
// Used when standalone JWT verification is disabled (e.g. behind Cerberus / Istio ExtAuthz).
func UserContextMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if UserIDFromContext(ctx) == "" {
				if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
					token := strings.TrimPrefix(authHeader, "Bearer ")
					if subject, err := ExtractSubjectFromJWT(token); err == nil && subject != "" {
						ctx = ContextWithUserID(ctx, subject)
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Middleware provides HTTP authentication handlers using a TokenVerifierInterface.
type Middleware struct {
	verifier TokenVerifierInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// Authenticate returns an HTTP middleware handler that validates Bearer JWT tokens.
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

			claims, err := m.verifier.VerifyToken(ctx, token)
			if err != nil {
				if errors.Is(err, ErrInvalidToken) {
					m.logger.Debugf("JWT verification failed: %v", err)
					m.unauthorizedResponse(w, "invalid token")
					return
				}
				m.unauthorizedResponse(w, "unauthorized")
				return
			}

			if claims != nil && claims.Subject != "" {
				ctx = ContextWithUserID(ctx, claims.Subject)
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

// NewMiddleware creates an authentication HTTP middleware instance.
func NewMiddleware(verifier TokenVerifierInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Middleware {
	return &Middleware{
		verifier: verifier,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
