package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	reflect "reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/token/jwt"
	"github.com/ory/hydra/v2/flow"
	"github.com/ory/hydra/v2/oauth2"
	"go.uber.org/mock/gomock"
)

//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_hooks.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package hooks -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go

func createHookRequest(clientId, userId string, grantTypes []string, aud []string) oauth2.TokenHookRequest {
	r := oauth2.TokenHookRequest{
		Session: &oauth2.Session{},
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

func createHookResponse(groups []string) *oauth2.TokenHookResponse {
	r := oauth2.TokenHookResponse{
		Session: *flow.NewConsentRequestSessionData(),
	}
	r.Session.AccessToken["groups"] = groups
	return &r
}

func TestHandleHydraHook(t *testing.T) {
	type serviceResult struct {
		r   []string
		err error
	}
	type authorizerResult struct {
		allowed bool
		err     error
	}

	groups := []string{"group1", "group2"}

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
			NewAPI(mockService, nil, mockTracer, mockMonitor, mockLogger).RegisterEndpoints(mux)
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
