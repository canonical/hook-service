// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/canonical/hook-service/pkg/groups"
)

func TestMiddleware_Authenticate(t *testing.T) {
	tests := []struct {
		name               string
		authHeader         string
		setupMocks         func(*gomock.Controller) TokenVerifierInterface
		expectedStatusCode int
		expectedBody       string
		expectedUserID     string
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
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "invalid-token").Return(nil, ErrInvalidToken)
				return mockVerifier
			},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "{\"message\":\"invalid token\",\"status\":401}\n",
		},
		{
			name:       "Valid token but unauthorized - rejects request",
			authHeader: "Bearer valid-token",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(nil, ErrUnauthorized)
				return mockVerifier
			},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "{\"message\":\"unauthorized\",\"status\":401}\n",
		},
		{
			name:       "Valid token without subject",
			authHeader: "Bearer valid-token",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(&Claims{}, nil)
				return mockVerifier
			},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:       "Valid token with subject sets user ID in context",
			authHeader: "Bearer valid-token",
			setupMocks: func(ctrl *gomock.Controller) TokenVerifierInterface {
				mockVerifier := NewMockTokenVerifierInterface(ctrl)
				mockVerifier.EXPECT().VerifyToken(gomock.Any(), "valid-token").Return(&Claims{Subject: "alice@example.com"}, nil)
				return mockVerifier
			},
			expectedStatusCode: http.StatusOK,
			expectedUserID:     "alice@example.com",
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

			var receivedUserID string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedUserID = groups.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("success"))
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

			if receivedUserID != tt.expectedUserID {
				t.Errorf("expected user ID %q in context, got %q", tt.expectedUserID, receivedUserID)
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

func TestUserContextMiddleware(t *testing.T) {
	validHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	validPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"jwt-user@example.com"}`))
	validJWT := validHeader + "." + validPayload + ".sig"

	tests := []struct {
		name           string
		headers        map[string]string
		expectedUserID string
	}{
		{
			name:           "no headers",
			headers:        nil,
			expectedUserID: "",
		},
		{
			name: "Authorization Bearer JWT header present",
			headers: map[string]string{
				"Authorization": "Bearer " + validJWT,
			},
			expectedUserID: "jwt-user@example.com",
		},
		{
			name: "Authorization header without Bearer prefix is ignored",
			headers: map[string]string{
				"Authorization": "Basic " + validJWT,
			},
			expectedUserID: "",
		},
		{
			name: "invalid Bearer JWT format is ignored",
			headers: map[string]string{
				"Authorization": "Bearer not-a-valid-jwt",
			},
			expectedUserID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recordedUserID string
			handler := UserContextMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordedUserID = groups.UserIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v0/groups", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if recordedUserID != tt.expectedUserID {
				t.Fatalf("expected user ID %q, got %q", tt.expectedUserID, recordedUserID)
			}
		})
	}
}

