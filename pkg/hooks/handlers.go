package hooks

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/go-chi/chi/v5"
	"github.com/ory/hydra/v2/flow"
	"github.com/ory/hydra/v2/oauth2"
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

func (a *API) handleHydraHook(w http.ResponseWriter, r *http.Request) {

	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.logger.Errorf("failed to read request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	req := new(oauth2.TokenHookRequest)
	err = json.Unmarshal(body, req)
	if err != nil {
		a.logger.Errorf("failed to parse request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	user := NewUserFromHookRequest(req, a.logger)

	groups, err := a.service.FetchUserGroups(r.Context(), *user)
	if err != nil {
		a.logger.Errorf("failed to fetch user groups: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	resp := oauth2.TokenHookResponse{
		Session: *flow.NewConsentRequestSessionData(),
	}
	resp.Session.AccessToken["groups"] = groups

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)

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
