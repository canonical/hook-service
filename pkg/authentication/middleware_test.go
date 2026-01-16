// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_tracer.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_verifier.go -source=./interfaces.go

func TestMiddleware_Authenticate(t *testing.T) {
	tests := []struct {
		name               string
		authHeader         string
		setupMocks         func(*gomock.Controller) (*gomock.Controller, TokenVerifierInterface, *oidc.IDToken, error)
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:       "Missing token - rejects request",
			authHeader: "",
			setupMocks: func(ctrl *gomock.Controller) (*gomock.Controller, TokenVerifierInterface, *oidc.IDToken, error) {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				return ctrl, mockVerifier, nil, nil
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Invalid token format - rejects request",
			authHeader: "InvalidToken",
			setupMocks: func(ctrl *gomock.Controller) (*gomock.Controller, TokenVerifierInterface, *oidc.IDToken, error) {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				return ctrl, mockVerifier, nil, nil
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Token verification fails - rejects request",
			authHeader: "Bearer invalid-token",
			setupMocks: func(ctrl *gomock.Controller) (*gomock.Controller, TokenVerifierInterface, *oidc.IDToken, error) {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "invalid-token").Return(nil, fmt.Errorf("invalid token"))
				return ctrl, mockVerifier, nil, fmt.Errorf("invalid token")
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Valid token but unauthorized - rejects request",
			authHeader: "Bearer valid-token",
			setupMocks: func(ctrl *gomock.Controller) (*gomock.Controller, TokenVerifierInterface, *oidc.IDToken, error) {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockToken := &oidc.IDToken{}
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(mockToken, nil)
				return ctrl, mockVerifier, mockToken, nil
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			ctx := context.Background()
			mockTracer.EXPECT().Start(gomock.Any(), "authentication.Middleware.Authenticate").Return(ctx, trace.SpanFromContext(ctx))

			_, mockVerifier, _, verifyErr := tt.setupMocks(ctrl)

			if tt.expectedStatusCode == http.StatusUnauthorized && verifyErr != nil {
				mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
			} else if tt.expectedStatusCode == http.StatusUnauthorized && tt.authHeader == "Bearer valid-token" {
				mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
				mockLogger.EXPECT().Debugf(gomock.Any()).AnyTimes()
				mockSecurityLogger := NewMockSecurityLoggerInterface(ctrl)
				mockLogger.EXPECT().Security().Return(mockSecurityLogger).AnyTimes()
				mockSecurityLogger.EXPECT().AuthzFailure(gomock.Any(), gomock.Any()).AnyTimes()
			}

			config := &Config{
				AllowedSubjects: []string{"test-subject"},
			}
			middleware := NewMiddleware(config, mockVerifier, mockTracer, mockMonitor, mockLogger)

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()

			middleware.Authenticate()(handler).ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatusCode {
				t.Errorf("expected status %d, got %d", tt.expectedStatusCode, rr.Code)
			}
			
			if tt.expectedBody != "" && rr.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, rr.Body.String())
			}
		})
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
			name:           "Raw token without Bearer prefix",
			authHeader:     "my-token-123",
			expectedToken:  "",
			expectedFound:  false,
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

			config := &Config{}
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
			config := NewConfig("https://issuer.example.com", test.allowedSubjects, "")

			if len(config.AllowedSubjects) != test.expectedSubjectsLen {
				t.Errorf("expected %d subjects, got %d", test.expectedSubjectsLen, len(config.AllowedSubjects))
			}

			if test.expectedSubjectsLen > 0 && config.AllowedSubjects[0] != test.expectedFirstSubject {
				t.Errorf("expected first subject %q, got %q", test.expectedFirstSubject, config.AllowedSubjects[0])
			}
		})
	}
}
