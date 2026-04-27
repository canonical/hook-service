// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	reflect "reflect"
	"testing"

	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/types"
	"github.com/go-chi/chi/v5"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/token/jwt"
	"github.com/ory/hydra/v2/flow"
	"github.com/ory/hydra/v2/oauth2"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func createHookRequest(clientId, userId string, grantTypes []string, aud []string) oauth2.TokenHookRequest {
	return createHookRequestWithExtra(clientId, userId, grantTypes, aud, nil)
}

// createHookRequestWithExtra builds a TokenHookRequest optionally populated
// with session extra data (e.g., {"_tenant_id": "..."}).
func createHookRequestWithExtra(clientId, userId string, grantTypes []string, aud []string, extra map[string]interface{}) oauth2.TokenHookRequest {
	r := oauth2.TokenHookRequest{
		Session: &oauth2.Session{
			Extra: extra,
		},
		Request: oauth2.Request{
			ClientID:        clientId,
			GrantTypes:      grantTypes,
			GrantedAudience: aud,
		},
	}
	if userId != "" {
		r.Session.DefaultSession = &openid.DefaultSession{
			Subject: userId,
			Claims:  &jwt.IDTokenClaims{Extra: map[string]interface{}{"email": 2134}},
		}
	}
	return r
}

func createHookResponse(groups []*types.Group) *oauth2.TokenHookResponse {
	r := oauth2.TokenHookResponse{
		Session: *flow.NewConsentRequestSessionData(),
	}
	groupIDs := make([]string, len(groups))
	for i, g := range groups {
		groupIDs[i] = g.ID
	}
	r.Session.AccessToken["groups"] = groupIDs
	return &r
}

func TestHandleHydraHook(t *testing.T) {
	type serviceResult struct {
		r   []*types.Group
		err error
	}
	type authorizerResult struct {
		allowed bool
		err     error
	}

	groups := []*types.Group{{ID: "group1", Name: "group1"}, {ID: "group2", Name: "group2"}}

	tests := []struct {
		name string

		userId     string
		clientId   string
		grantTypes []string
		grantedAud []string

		fetchUsersResult       *serviceResult
		authorizeRequestResult *authorizerResult

		expectedStatus   int
		expectedResponse *oauth2.TokenHookResponse
	}{
		{
			name:                   "Should add groups to user",
			userId:                 "user",
			clientId:               "client",
			grantTypes:             []string{"authorization_code"},
			fetchUsersResult:       &serviceResult{r: groups},
			authorizeRequestResult: &authorizerResult{allowed: true},
			expectedStatus:         http.StatusOK,
			expectedResponse:       createHookResponse(groups),
		},
		{
			name:                   "Should add groups to client when using client_credentials",
			clientId:               "client",
			grantTypes:             []string{"client_credentials"},
			grantedAud:             []string{"client"},
			fetchUsersResult:       &serviceResult{r: groups},
			authorizeRequestResult: &authorizerResult{allowed: true},
			expectedStatus:         http.StatusOK,
			expectedResponse:       createHookResponse(groups),
		},
		{
			name:                   "Should add groups to client when using jwt bearer",
			clientId:               "client",
			grantTypes:             []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			grantedAud:             []string{"client"},
			fetchUsersResult:       &serviceResult{r: groups},
			authorizeRequestResult: &authorizerResult{allowed: true},
			expectedStatus:         http.StatusOK,
			expectedResponse:       createHookResponse(groups),
		},
		{
			name:                   "Should fail authz",
			clientId:               "client",
			grantTypes:             []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			grantedAud:             []string{"client"},
			fetchUsersResult:       &serviceResult{r: groups},
			authorizeRequestResult: &authorizerResult{allowed: false},
			expectedStatus:         http.StatusForbidden,
		},
		{
			name:             "Should fail on error",
			userId:           "user",
			clientId:         "client",
			grantTypes:       []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			fetchUsersResult: &serviceResult{err: errors.New("some error")},
			expectedStatus:   http.StatusForbidden,
		},
		{
			name:                   "Should fail on authz error",
			userId:                 "user",
			clientId:               "client",
			grantTypes:             []string{"authorization_code"},
			fetchUsersResult:       &serviceResult{r: groups},
			authorizeRequestResult: &authorizerResult{err: errors.New("some error")},
			expectedStatus:         http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockService := NewMockServiceInterface(ctrl)
			mockTenantValidator := NewMockTenantValidatorInterface(ctrl)

			// Mock tracer Start call
			mockTracer.EXPECT().Start(gomock.Any(), "hooks.API.handleHydraHook").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)

			if test.fetchUsersResult != nil {
				mockService.EXPECT().FetchUserGroups(gomock.Any(), gomock.Any()).Times(1).Return(test.fetchUsersResult.r, test.fetchUsersResult.err)
			}

			if test.authorizeRequestResult != nil {
				mockService.EXPECT().AuthorizeRequest(
					gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
				).Times(1).Return(test.authorizeRequestResult.allowed, test.authorizeRequestResult.err)
			}

			mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
			if test.expectedStatus != http.StatusOK {
				mockLogger.EXPECT().Error(gomock.Any(), gomock.Any()).AnyTimes()
				mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
				mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
			}

			body, _ := json.Marshal(createHookRequest(test.clientId, test.userId, test.grantTypes, test.grantedAud))
			req := httptest.NewRequest(http.MethodPost, "/api/v0/hook/hydra", bytes.NewBuffer(body))

			mux := chi.NewMux()
			NewAPI(mockService, mockTenantValidator, nil, 100, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)
			res := w.Result()

			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)

			if err != nil {
				t.Fatalf("expected error to be nil got %v", err)
			}

			if test.expectedResponse != nil {
				resp := new(oauth2.TokenHookResponse)
				if err := json.Unmarshal(data, resp); err != nil {
					t.Fatalf("expected error to be nil got %v", err)
				}

				if reflect.DeepEqual(resp.Session, test.expectedResponse.Session) {
					t.Fatalf("expected response body to be %v got %v", test.expectedResponse, *resp)
				}
			}

			if res.StatusCode != test.expectedStatus {
				t.Fatalf("expected status to be %v not %v", test.expectedStatus, res.StatusCode)
			}
		})
	}
}

func TestHandleHydraHookTenantValidation(t *testing.T) {
	groups := []*types.Group{{ID: "group1", Name: "group1"}}

	tests := []struct {
		name string

		userId     string
		clientId   string
		grantTypes []string
		extra      map[string]interface{}

		tenantValidatorResult error

		expectedStatus   int
		expectedTenantID string
	}{
		{
			name:                  "No tenant_id in session — skip validation",
			userId:                "user-id",
			clientId:              "client",
			grantTypes:            []string{"authorization_code"},
			extra:                 nil,
			tenantValidatorResult: nil,
			expectedStatus:        http.StatusOK,
		},
		{
			name:                  "Valid tenant membership — inject tenant_id",
			userId:                "user-id",
			clientId:              "client",
			grantTypes:            []string{"authorization_code"},
			extra:                 map[string]interface{}{"_tenant_id": "tenant-abc"},
			tenantValidatorResult: nil,
			expectedStatus:        http.StatusOK,
			expectedTenantID:      "tenant-abc",
		},
		{
			name:                  "User not a member — 403",
			userId:                "user-id",
			clientId:              "client",
			grantTypes:            []string{"authorization_code"},
			extra:                 map[string]interface{}{"_tenant_id": "tenant-abc"},
			tenantValidatorResult: tenants.ErrNotMember,
			expectedStatus:        http.StatusForbidden,
		},
		{
			name:                  "Tenant-service unreachable — 500",
			userId:                "user-id",
			clientId:              "client",
			grantTypes:            []string{"authorization_code"},
			extra:                 map[string]interface{}{"_tenant_id": "tenant-abc"},
			tenantValidatorResult: errors.New("cannot reach tenant-service"),
			expectedStatus:        http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockLogger := NewMockLoggerInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockService := NewMockServiceInterface(ctrl)
			mockTenantValidator := NewMockTenantValidatorInterface(ctrl)

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.API.handleHydraHook").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)

			mockService.EXPECT().FetchUserGroups(gomock.Any(), gomock.Any()).Times(1).Return(groups, nil)
			mockService.EXPECT().AuthorizeRequest(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(true, nil)

			hasTenant := test.extra != nil && test.extra["_tenant_id"] != nil
			if hasTenant {
				mockTenantValidator.EXPECT().ValidateMembership(gomock.Any(), test.userId, test.extra["_tenant_id"].(string)).Times(1).Return(test.tenantValidatorResult)
			}

			mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()

			body, _ := json.Marshal(createHookRequestWithExtra(test.clientId, test.userId, test.grantTypes, nil, test.extra))
			req := httptest.NewRequest(http.MethodPost, "/api/v0/hook/hydra", bytes.NewBuffer(body))

			mux := chi.NewMux()
			NewAPI(mockService, mockTenantValidator, nil, 100, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)
			res := w.Result()
			defer res.Body.Close()

			if res.StatusCode != test.expectedStatus {
				t.Fatalf("expected status %d, got %d", test.expectedStatus, res.StatusCode)
			}

			if test.expectedTenantID != "" {
				data, _ := io.ReadAll(res.Body)
				resp := new(oauth2.TokenHookResponse)
				if err := json.Unmarshal(data, resp); err != nil {
					t.Fatalf("expected error to be nil got %v", err)
				}
				tid, ok := resp.Session.AccessToken["tenant_id"].(string)
				if !ok || tid != test.expectedTenantID {
					t.Fatalf("expected tenant_id %q in access token, got %q", test.expectedTenantID, tid)
				}
				tid, ok = resp.Session.IDToken["tenant_id"].(string)
				if !ok || tid != test.expectedTenantID {
					t.Fatalf("expected tenant_id %q in id token, got %q", test.expectedTenantID, tid)
				}
			}
		})
	}
}

func TestExtractTenantID(t *testing.T) {
	tests := []struct {
		name     string
		req      *oauth2.TokenHookRequest
		expected string
	}{
		{
			name:     "nil session",
			req:      &oauth2.TokenHookRequest{},
			expected: "",
		},
		{
			name:     "nil extra",
			req:      &oauth2.TokenHookRequest{Session: &oauth2.Session{}},
			expected: "",
		},
		{
			name:     "no _tenant_id key",
			req:      &oauth2.TokenHookRequest{Session: &oauth2.Session{Extra: map[string]interface{}{"foo": "bar"}}},
			expected: "",
		},
		{
			name:     "_tenant_id present",
			req:      &oauth2.TokenHookRequest{Session: &oauth2.Session{Extra: map[string]interface{}{"_tenant_id": "t-123"}}},
			expected: "t-123",
		},
		{
			name:     "_tenant_id wrong type",
			req:      &oauth2.TokenHookRequest{Session: &oauth2.Session{Extra: map[string]interface{}{"_tenant_id": 42}}},
			expected: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractTenantID(test.req)
			if got != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, got)
			}
		})
	}
}

// TestHandleHydraHookSemaphore verifies that handleHydraHook returns 429 when
// the semaphore is exhausted (maxConcurrent=0 means capacity-0 channel, which
// always takes the default branch in a non-blocking select).
func TestHandleHydraHookSemaphore(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLogger := NewMockLoggerInterface(ctrl)
	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockService := NewMockServiceInterface(ctrl)
	mockTenantValidator := NewMockTenantValidatorInterface(ctrl)

	mux := chi.NewMux()
	NewAPI(mockService, mockTenantValidator, nil, 0, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/hook/hydra", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}
}
