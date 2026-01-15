// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_tracer.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_verifier.go -source=./interfaces.go

func TestMiddleware_Authenticate_Disabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockVerifier := NewMockTokenVerifierInterface(ctrl)

	// When auth is disabled, tracer is still called
	ctx := context.Background()
	mockTracer.EXPECT().Start(gomock.Any(), "authentication.Middleware.Authenticate").Return(ctx, trace.SpanFromContext(ctx))

	config := &Config{Enabled: false}
	middleware := NewMiddleware(config, mockVerifier, mockTracer, mockMonitor, mockLogger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	middleware.Authenticate()(handler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status OK, got %d", rr.Code)
	}
}

func TestMiddleware_Authenticate_MissingToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockVerifier := NewMockTokenVerifierInterface(ctrl)

	ctx := context.Background()
	mockTracer.EXPECT().Start(gomock.Any(), "authentication.Middleware.Authenticate").Return(ctx, trace.SpanFromContext(ctx))

	config := &Config{Enabled: true, AllowedSubjects: []string{"test-subject"}}
	middleware := NewMiddleware(config, mockVerifier, mockTracer, mockMonitor, mockLogger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	middleware.Authenticate()(handler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status Unauthorized, got %d", rr.Code)
	}
}

func TestMiddleware_GetBearerToken(t *testing.T) {
	tests := []struct {
		name           string
		authHeader     string
		expectedToken  string
		expectedFound  bool
	}{
		{
			name:           "No Authorization header",
			authHeader:     "",
			expectedToken:  "",
			expectedFound:  false,
		},
		{
			name:           "Bearer token",
			authHeader:     "Bearer my-token-123",
			expectedToken:  "my-token-123",
			expectedFound:  true,
		},
		{
			name:           "Raw token",
			authHeader:     "my-token-123",
			expectedToken:  "my-token-123",
			expectedFound:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)
			mockVerifier := NewMockTokenVerifierInterface(ctrl)

			config := &Config{Enabled: true}
			middleware := NewMiddleware(config, mockVerifier, mockTracer, mockMonitor, mockLogger)

			headers := http.Header{}
			if test.authHeader != "" {
				headers.Set("Authorization", test.authHeader)
			}

			token, found := middleware.getBearerToken(headers)

			if token != test.expectedToken {
				t.Errorf("expected token %q, got %q", test.expectedToken, token)
			}
			if found != test.expectedFound {
				t.Errorf("expected found %v, got %v", test.expectedFound, found)
			}
		})
	}
}

func TestConfig_NewConfig(t *testing.T) {
	tests := []struct {
		name                    string
		allowedSubjects         string
		expectedSubjectsLen     int
		expectedFirstSubject    string
	}{
		{
			name:                 "Empty subjects",
			allowedSubjects:      "",
			expectedSubjectsLen:  0,
		},
		{
			name:                 "Single subject",
			allowedSubjects:      "subject-1",
			expectedSubjectsLen:  1,
			expectedFirstSubject: "subject-1",
		},
		{
			name:                 "Multiple subjects",
			allowedSubjects:      "subject-1,subject-2,subject-3",
			expectedSubjectsLen:  3,
			expectedFirstSubject: "subject-1",
		},
		{
			name:                 "Subjects with spaces",
			allowedSubjects:      "subject-1, subject-2 , subject-3",
			expectedSubjectsLen:  3,
			expectedFirstSubject: "subject-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := NewConfig(true, "https://issuer.example.com", test.allowedSubjects, "")

			if len(config.AllowedSubjects) != test.expectedSubjectsLen {
				t.Errorf("expected %d subjects, got %d", test.expectedSubjectsLen, len(config.AllowedSubjects))
			}

			if test.expectedSubjectsLen > 0 && config.AllowedSubjects[0] != test.expectedFirstSubject {
				t.Errorf("expected first subject %q, got %q", test.expectedFirstSubject, config.AllowedSubjects[0])
			}
		})
	}
}

