// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package hooks

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/internal/types"
	"github.com/go-chi/chi/v5"
	"github.com/ory/hydra/v2/flow"
	"github.com/ory/hydra/v2/oauth2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	GrantTypeClientCredentials string = "client_credentials"
	GrantTypeJWTBearer         string = "urn:ietf:params:oauth:grant-type:jwt-bearer"
)

type API struct {
	service    ServiceInterface
	middleware *AuthMiddleware

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// RegisterEndpoints registers the Hydra token hook endpoint on the given router.
func (a *API) RegisterEndpoints(mux *chi.Mux) {
	if a.middleware != nil {
		mux = mux.With(a.middleware.AuthMiddleware).(*chi.Mux)
	}
	mux.Post("/api/v0/hook/hydra", a.handleHydraHook)
}

// handleHydraHook processes OAuth token hook requests from Hydra.
// It delegates orchestration to the service layer and maps the result to
// an HTTP response. Span attributes include user.id, client.id, grant_types,
// groups.count, tenant_id, and http.status_code for observability.
func (a *API) handleHydraHook(w http.ResponseWriter, r *http.Request) {
	ctx, span := a.tracer.Start(r.Context(), "hooks.API.handleHydraHook")
	defer span.End()

	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.logger.Errorf("failed to read request body: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to read request body")
		span.SetAttributes(attribute.Int("http.status_code", http.StatusBadRequest))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	req := new(oauth2.TokenHookRequest)
	if err = json.Unmarshal(body, req); err != nil {
		a.logger.Errorf("failed to parse request: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse request")
		span.SetAttributes(attribute.Int("http.status_code", http.StatusBadRequest))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user := NewUserFromHookRequest(req, a.logger)

	span.SetAttributes(
		attribute.String("user.id", user.GetUserId()),
		attribute.String("client.id", req.Request.ClientID),
		attribute.StringSlice("grant_types", req.Request.GrantTypes),
		attribute.StringSlice("granted_audience", req.Request.GrantedAudience),
	)

	hctx, err := a.service.ProcessRequest(ctx, *user, *req)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooBusy):
			span.SetAttributes(attribute.Int("http.status_code", http.StatusTooManyRequests))
			w.WriteHeader(http.StatusTooManyRequests)
		case errors.Is(err, tenants.ErrNotMember):
			a.logger.Infof("tenant membership denied: %v", err)
			span.SetStatus(codes.Error, "tenant membership denied")
			span.SetAttributes(attribute.Int("http.status_code", http.StatusForbidden))
			w.WriteHeader(http.StatusForbidden)
		case errors.Is(err, errTenantInternal):
			a.logger.Errorf("failed to validate tenant membership: %v", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "tenant validation failed")
			span.SetAttributes(attribute.Int("http.status_code", http.StatusInternalServerError))
			w.WriteHeader(http.StatusInternalServerError)
		default:
			a.logger.Errorf("failed to process hook request: %v", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to process hook request")
			span.SetAttributes(attribute.Int("http.status_code", http.StatusForbidden))
			w.WriteHeader(http.StatusForbidden)
		}
		return
	}

	span.SetAttributes(attribute.Int("groups.count", len(hctx.Groups)))
	if hctx.TenantID != "" {
		span.SetAttributes(attribute.String("tenant_id", hctx.TenantID))
	}

	encoded, err := json.Marshal(a.composeTokenResponse(hctx))
	if err != nil {
		a.logger.Errorf("failed to encode hook response: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to encode hook response")
		span.SetAttributes(attribute.Int("http.status_code", http.StatusInternalServerError))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	span.SetAttributes(attribute.Int("http.status_code", http.StatusOK))
	span.SetStatus(codes.Ok, "request successful")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// composeTokenResponse builds the final TokenHookResponse from a processed hook
// context, including group names and the tenant_id when present.
func (a *API) composeTokenResponse(hctx *HookContext) *oauth2.TokenHookResponse {
	resp := a.newHookResponse(hctx.Groups)
	if hctx.TenantID != "" {
		resp.Session.AccessToken["tenant_id"] = hctx.TenantID
		resp.Session.IDToken["tenant_id"] = hctx.TenantID
	}
	return resp
}

// newHookResponse creates a TokenHookResponse with the group names added to both
// the access token and ID token session data. Duplicate group names are removed.
func (a *API) newHookResponse(groups []*types.Group) *oauth2.TokenHookResponse {
	resp := oauth2.TokenHookResponse{
		Session: *flow.NewConsentRequestSessionData(),
	}

	if len(groups) == 0 {
		return &resp
	}

	groupNames := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupNames[g.Name] = struct{}{}
	}

	gg := slices.Collect(maps.Keys(groupNames))
	resp.Session.AccessToken["groups"] = gg
	resp.Session.IDToken["groups"] = gg
	return &resp
}

// NewAPI creates a new API handler for the Hydra token hook endpoint.
func NewAPI(
	service ServiceInterface,
	middleware *AuthMiddleware,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *API {
	a := new(API)

	a.service = service
	if middleware != nil {
		a.middleware = middleware
	}

	a.monitor = monitor
	a.tracer = tracer
	a.logger = logger

	return a
}
