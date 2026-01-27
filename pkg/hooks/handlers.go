// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package hooks

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
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

func (a *API) RegisterEndpoints(mux *chi.Mux) {
	if a.middleware != nil {
		mux = mux.With(a.middleware.AuthMiddleware).(*chi.Mux)
	}
	mux.Post("/api/v0/hook/hydra", a.handleHydraHook)
}

// handleHydraHook processes OAuth token hook requests from Hydra.
// It enriches tokens with user groups and enforces authorization policies.
// Span attributes include user.id, client.id, grant_types, groups.count,
// authorization.allowed, and http.status_code for observability.
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
	err = json.Unmarshal(body, req)
	if err != nil {
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

	groups, err := a.service.FetchUserGroups(ctx, *user)
	if err != nil {
		a.logger.Errorf("failed to fetch user groups: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to fetch user groups")
		span.SetAttributes(attribute.Int("http.status_code", http.StatusForbidden))
		w.WriteHeader(http.StatusForbidden)
		return
	}

	span.SetAttributes(attribute.Int("groups.count", len(groups)))

	allowed, err := a.service.AuthorizeRequest(ctx, *user, *req, groups)
	if err != nil {
		a.logger.Errorf("failed to authorize request: %v", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to authorize request")
		span.SetAttributes(attribute.Int("http.status_code", http.StatusForbidden))
		w.WriteHeader(http.StatusForbidden)
		return
	}

	span.SetAttributes(attribute.Bool("authorization.allowed", allowed))

	if !allowed {
		a.logger.Infof("unauthorized request, user %s tried to access %s", user.GetUserId(), req.Request.ClientID)
		span.SetStatus(codes.Error, "authorization denied")
		span.SetAttributes(attribute.Int("http.status_code", http.StatusForbidden))
		w.WriteHeader(http.StatusForbidden)
		return
	}

	resp := a.newHookResponse(groups)

	span.SetAttributes(attribute.Int("http.status_code", http.StatusOK))
	span.SetStatus(codes.Ok, "request successful")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)

}

// newHookResponse creates a TokenHookResponse with the group names added to both
// the access token and ID token session data. Duplicate group names are removed.
func (a *API) newHookResponse(groups []*types.Group) *oauth2.TokenHookResponse {
	resp := oauth2.TokenHookResponse{
		Session: *flow.NewConsentRequestSessionData(),
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
