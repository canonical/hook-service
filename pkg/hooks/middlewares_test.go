// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go

func TestMiddleware_Auth(t *testing.T) {
	tests := []struct {
		name         string
		apiToken     string
		requestToken string

		expectedStatus int
	}{
		{
			name:           "Should fail because no token provided",
			apiToken:       "token",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Should fail because of invalid token provided",
			apiToken:       "token",
			requestToken:   "invalid_token",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Should pass",
			apiToken:       "token",
			requestToken:   "token",
			expectedStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockTracer := NewMockTracingInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			if test.expectedStatus != http.StatusOK {
				mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()
			}

			middleware := NewAuthMiddleware(test.apiToken, mockTracer, mockLogger)

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("handler\n"))
			})
			m := applyMiddlewares(handler, middleware.AuthMiddleware)

			r := httptest.NewRequest(http.MethodPost, "/api/v0/protected", nil)
			if test.requestToken != "" {
				r.Header.Add("Authorization", test.requestToken)
			}

			mockResponse := httptest.NewRecorder()

			m.ServeHTTP(mockResponse, r)

			response := mockResponse.Result()

			if response.StatusCode != test.expectedStatus {
				t.Fatalf("expected status %d, got %d", test.expectedStatus, response.StatusCode)
			}

		})
	}
}

func applyMiddlewares(handler http.Handler, ms ...func(http.Handler) http.Handler) http.Handler {
	for i := len(ms) - 1; i >= 0; i-- {
		handler = ms[i](handler)
	}
	return handler
}
