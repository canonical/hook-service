// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

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

func createHookRequestWithIDTokenExtra(clientId, userId string, grantTypes []string, idTokenExtra map[string]interface{}) oauth2.TokenHookRequest {
	return oauth2.TokenHookRequest{
		Session: &oauth2.Session{
			DefaultSession: &openid.DefaultSession{
				Subject: userId,
				Claims:  &jwt.IDTokenClaims{Extra: idTokenExtra},
			},
		},
		Request: oauth2.Request{
			ClientID:   clientId,
			GrantTypes: grantTypes,
		},
	}
}

func createHookRequestWithSessionAndIDTokenExtra(clientId, userId string, grantTypes []string, sessionExtra, idTokenExtra map[string]interface{}) oauth2.TokenHookRequest {
	return oauth2.TokenHookRequest{
		Session: &oauth2.Session{
			Extra: sessionExtra,
			DefaultSession: &openid.DefaultSession{
				Subject: userId,
				Claims:  &jwt.IDTokenClaims{Extra: idTokenExtra},
			},
		},
		Request: oauth2.Request{
			ClientID:   clientId,
			GrantTypes: grantTypes,
		},
	}
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
	groups := []*types.Group{{ID: "group1", Name: "group1"}, {ID: "group2", Name: "group2"}}

	tests := []struct {
		name string

		userId     string
		clientId   string
		grantTypes []string
		grantedAud []string

		processRequestResult *HookContext
		processRequestError  error

		expectedStatus   int
		expectedResponse *oauth2.TokenHookResponse
		request          *oauth2.TokenHookRequest
		assertResponse   func(t *testing.T, resp *oauth2.TokenHookResponse)
	}{
		{
			name:                 "Should add groups to user",
			userId:               "user",
			clientId:             "client",
			grantTypes:           []string{"authorization_code"},
			processRequestResult: &HookContext{Groups: groups},
			expectedStatus:       http.StatusOK,
			expectedResponse:     createHookResponse(groups),
		},
		{
			name:                 "Should add groups to client when using client_credentials",
			clientId:             "client",
			grantTypes:           []string{"client_credentials"},
			grantedAud:           []string{"client"},
			processRequestResult: &HookContext{Groups: groups},
			expectedStatus:       http.StatusOK,
			expectedResponse:     createHookResponse(groups),
		},
		{
			name:                 "Should add groups to client when using jwt bearer",
			clientId:             "client",
			grantTypes:           []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			grantedAud:           []string{"client"},
			processRequestResult: &HookContext{Groups: groups},
			expectedStatus:       http.StatusOK,
			expectedResponse:     createHookResponse(groups),
		},
		{
			name:                 "Should preserve existing ID token claims in hook response",
			processRequestResult: &HookContext{Groups: []*types.Group{{ID: "g1", Name: "g1"}}},
			expectedStatus:       http.StatusOK,
			request: ptr(createHookRequestWithIDTokenExtra(
				"client",
				"user",
				[]string{"authorization_code"},
				map[string]interface{}{"email": "user@example.com", "name": "Alice"},
			)),
			assertResponse: func(t *testing.T, resp *oauth2.TokenHookResponse) {
				email, ok := resp.Session.IDToken["email"].(string)
				if !ok || email != "user@example.com" {
					t.Fatalf("expected id_token email to be %q, got %v", "user@example.com", resp.Session.IDToken["email"])
				}

				name, ok := resp.Session.IDToken["name"].(string)
				if !ok || name != "Alice" {
					t.Fatalf("expected id_token name to be %q, got %v", "Alice", resp.Session.IDToken["name"])
				}

				if resp.Session.IDToken["groups"] == nil {
					t.Fatal("expected id_token groups to be non-nil")
				}
			},
		},
		{
			name:                 "Should overwrite stale groups claim while preserving other id token claims",
			processRequestResult: &HookContext{Groups: []*types.Group{{ID: "g1", Name: "fresh-group"}}},
			expectedStatus:       http.StatusOK,
			request: ptr(createHookRequestWithIDTokenExtra(
				"client",
				"user",
				[]string{"authorization_code"},
				map[string]interface{}{"groups": []string{"stale-group"}, "email": "user@example.com"},
			)),
			assertResponse: func(t *testing.T, resp *oauth2.TokenHookResponse) {
				email, ok := resp.Session.IDToken["email"].(string)
				if !ok || email != "user@example.com" {
					t.Fatalf("expected id_token email to be %q, got %v", "user@example.com", resp.Session.IDToken["email"])
				}

				groupsClaim, ok := resp.Session.IDToken["groups"].([]interface{})
				if !ok {
					t.Fatalf("expected id_token groups claim to be []interface{}, got %T", resp.Session.IDToken["groups"])
				}

				hasFresh := false
				hasStale := false
				for _, g := range groupsClaim {
					groupName, ok := g.(string)
					if !ok {
						continue
					}
					if groupName == "fresh-group" {
						hasFresh = true
					}
					if groupName == "stale-group" {
						hasStale = true
					}
				}

				if !hasFresh {
					t.Fatal("expected id_token groups to include fresh-group")
				}
				if hasStale {
					t.Fatal("expected id_token groups to not include stale-group")
				}
			},
		},
		{
			name:                 "Should preserve existing access token claims in hook response",
			processRequestResult: &HookContext{Groups: []*types.Group{{ID: "g1", Name: "g1"}}},
			expectedStatus:       http.StatusOK,
			request: ptr(createHookRequestWithSessionAndIDTokenExtra(
				"client",
				"user",
				[]string{"authorization_code"},
				map[string]interface{}{"scope": "openid", "aud": "app"},
				map[string]interface{}{"email": "user@example.com"},
			)),
			assertResponse: func(t *testing.T, resp *oauth2.TokenHookResponse) {
				scope, ok := resp.Session.AccessToken["scope"].(string)
				if !ok || scope != "openid" {
					t.Fatalf("expected access_token scope to be %q, got %v", "openid", resp.Session.AccessToken["scope"])
				}

				aud, ok := resp.Session.AccessToken["aud"].(string)
				if !ok || aud != "app" {
					t.Fatalf("expected access_token aud to be %q, got %v", "app", resp.Session.AccessToken["aud"])
				}

				if resp.Session.AccessToken["groups"] == nil {
					t.Fatal("expected access_token groups to be non-nil")
				}
			},
		},
		{
			name:                 "Should overwrite stale groups claim while preserving other access token claims",
			processRequestResult: &HookContext{Groups: []*types.Group{{ID: "g1", Name: "fresh-group"}}},
			expectedStatus:       http.StatusOK,
			request: ptr(createHookRequestWithSessionAndIDTokenExtra(
				"client",
				"user",
				[]string{"authorization_code"},
				map[string]interface{}{"groups": []string{"stale-group"}, "scope": "openid"},
				map[string]interface{}{"email": "user@example.com"},
			)),
			assertResponse: func(t *testing.T, resp *oauth2.TokenHookResponse) {
				scope, ok := resp.Session.AccessToken["scope"].(string)
				if !ok || scope != "openid" {
					t.Fatalf("expected access_token scope to be %q, got %v", "openid", resp.Session.AccessToken["scope"])
				}

				groupsClaim, ok := resp.Session.AccessToken["groups"].([]interface{})
				if !ok {
					t.Fatalf("expected access_token groups claim to be []interface{}, got %T", resp.Session.AccessToken["groups"])
				}

				hasFresh := false
				hasStale := false
				for _, g := range groupsClaim {
					groupName, ok := g.(string)
					if !ok {
						continue
					}
					if groupName == "fresh-group" {
						hasFresh = true
					}
					if groupName == "stale-group" {
						hasStale = true
					}
				}

				if !hasFresh {
					t.Fatal("expected access_token groups to include fresh-group")
				}
				if hasStale {
					t.Fatal("expected access_token groups to not include stale-group")
				}
			},
		},
		{
			name:                 "Should not panic when default session is nil",
			processRequestResult: &HookContext{Groups: []*types.Group{{ID: "g1", Name: "g1"}}},
			expectedStatus:       http.StatusOK,
			request: &oauth2.TokenHookRequest{
				Session: &oauth2.Session{},
				Request: oauth2.Request{
					ClientID:   "client",
					GrantTypes: []string{"authorization_code"},
				},
			},
			assertResponse: func(t *testing.T, resp *oauth2.TokenHookResponse) {
				if resp.Session.IDToken["groups"] == nil {
					t.Fatal("expected id_token groups to be non-nil")
				}
			},
		},
		{
			name:                "Should fail authz",
			clientId:            "client",
			grantTypes:          []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			grantedAud:          []string{"client"},
			processRequestError: errors.New("access denied"),
			expectedStatus:      http.StatusForbidden,
		},
		{
			name:                "Should fail on error",
			userId:              "user",
			clientId:            "client",
			grantTypes:          []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
			processRequestError: errors.New("cannot fetch user groups: some error"),
			expectedStatus:      http.StatusForbidden,
		},
		{
			name:                "Should return 429 when pool is full",
			userId:              "user",
			clientId:            "client",
			grantTypes:          []string{"authorization_code"},
			processRequestError: ErrTooBusy,
			expectedStatus:      http.StatusTooManyRequests,
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

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.API.handleHydraHook").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)

			mockService.EXPECT().ProcessRequest(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(1).Return(test.processRequestResult, test.processRequestError)

			mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
			if test.expectedStatus != http.StatusOK {
				mockLogger.EXPECT().Error(gomock.Any(), gomock.Any()).AnyTimes()
				mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
				mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
			}

			reqPayload := createHookRequest(test.clientId, test.userId, test.grantTypes, test.grantedAud)
			if test.request != nil {
				reqPayload = *test.request
			}

			body, _ := json.Marshal(reqPayload)
			req := httptest.NewRequest(http.MethodPost, "/api/v0/hook/hydra", bytes.NewBuffer(body))

			mux := chi.NewMux()
			NewAPI(mockService, nil, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)
			res := w.Result()

			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)

			if err != nil {
				t.Fatalf("expected error to be nil got %v", err)
			}

			if test.expectedResponse != nil || test.assertResponse != nil {
				resp := new(oauth2.TokenHookResponse)
				if err := json.Unmarshal(data, resp); err != nil {
					t.Fatalf("expected error to be nil got %v", err)
				}

				if test.expectedResponse != nil {
					if reflect.DeepEqual(resp.Session, test.expectedResponse.Session) {
						t.Fatalf("expected response body to be %v got %v", test.expectedResponse, *resp)
					}
				}

				if test.assertResponse != nil {
					test.assertResponse(t, resp)
				}
			}

			if res.StatusCode != test.expectedStatus {
				t.Fatalf("expected status to be %v not %v", test.expectedStatus, res.StatusCode)
			}
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestHandleHydraHookTenantValidation(t *testing.T) {
	groups := []*types.Group{{ID: "group1", Name: "group1"}}

	tests := []struct {
		name string

		userId     string
		clientId   string
		grantTypes []string

		processRequestResult *HookContext
		processRequestError  error

		expectedStatus   int
		expectedTenantID string
	}{
		{
			name:                 "No tenant_id in session — skip validation",
			userId:               "user-id",
			clientId:             "client",
			grantTypes:           []string{"authorization_code"},
			processRequestResult: &HookContext{Groups: groups},
			expectedStatus:       http.StatusOK,
		},
		{
			name:                 "Valid tenant membership — inject tenant_id",
			userId:               "user-id",
			clientId:             "client",
			grantTypes:           []string{"authorization_code"},
			processRequestResult: &HookContext{Groups: groups, TenantID: "tenant-abc"},
			expectedStatus:       http.StatusOK,
			expectedTenantID:     "tenant-abc",
		},
		{
			name:                "User not a member — 403",
			userId:              "user-id",
			clientId:            "client",
			grantTypes:          []string{"authorization_code"},
			processRequestError: tenants.ErrNotMember,
			expectedStatus:      http.StatusForbidden,
		},
		{
			name:                "Tenant-service unreachable — 500",
			userId:              "user-id",
			clientId:            "client",
			grantTypes:          []string{"authorization_code"},
			processRequestError: errTenantInternal,
			expectedStatus:      http.StatusInternalServerError,
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

			mockTracer.EXPECT().Start(gomock.Any(), "hooks.API.handleHydraHook").Return(context.Background(), trace.SpanFromContext(context.Background())).Times(1)

			mockService.EXPECT().ProcessRequest(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(1).Return(test.processRequestResult, test.processRequestError)

			mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
			mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()

			body, _ := json.Marshal(createHookRequestWithExtra(test.clientId, test.userId, test.grantTypes, nil, nil))
			req := httptest.NewRequest(http.MethodPost, "/api/v0/hook/hydra", bytes.NewBuffer(body))

			mux := chi.NewMux()
			NewAPI(mockService, nil, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)
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
