// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type Middleware struct {
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

			authorized, err := m.verifier.VerifyToken(ctx, token)
			if err != nil {
				m.logger.Debugf("JWT verification failed: %v", err)
				m.unauthorizedResponse(w, "invalid token")
				return
			}

			if !authorized {
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

func NewMiddleware(verifier TokenVerifierInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Middleware {
	return &Middleware{
		verifier: verifier,
		tracer:   tracer,
		monitor:  monitor,
		logger:   logger,
	}
}
