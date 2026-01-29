// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"context"
	"fmt"
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

func TestMiddleware_Authenticate(t *testing.T) {
	tests := []struct {
		name               string
		authHeader         string
		setupMocks         func(*gomock.Controller) TokenVerifierInterface
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:       "Missing token - rejects request",
			authHeader: "",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				return mockVerifier
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Invalid token format - rejects request",
			authHeader: "InvalidToken",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				return mockVerifier
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Token verification fails - rejects request",
			authHeader: "Bearer invalid-token",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "invalid-token").Return(false, fmt.Errorf("invalid token"))
				return mockVerifier
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Valid token but unauthorized - rejects request",
			authHeader: "Bearer valid-token",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(false, nil)
				return mockVerifier
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:       "Valid token",
			authHeader: "Bearer valid-token",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(true, nil)
				return mockVerifier
			},
			expectedStatusCode: http.StatusOK,
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
			mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()

			mockVerifier := tt.setupMocks(ctrl)

			middleware := NewMiddleware(mockVerifier, mockTracer, mockMonitor, mockLogger)

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
		name          string
		authHeader    string
		expectedToken string
		expectedFound bool
	}{
		{
			name:          "No Authorization header",
			authHeader:    "",
			expectedToken: "",
			expectedFound: false,
		},
		{
			name:          "Bearer token",
			authHeader:    "Bearer my-token-123",
			expectedToken: "my-token-123",
			expectedFound: true,
		},
		{
			name:          "Raw token without Bearer prefix",
			authHeader:    "my-token-123",
			expectedToken: "",
			expectedFound: false,
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

			middleware := NewMiddleware(mockVerifier, mockTracer, mockMonitor, mockLogger)

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
