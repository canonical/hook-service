package authorization

import (
	"encoding/json"
	"net/http"

	"github.com/canonical/hook-service/internal/http/types"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"

	"github.com/go-chi/chi/v5"
)

type App struct {
	ClientID string `json:"client_id"`
}

type API struct {
	service ServiceInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (a *API) RegisterEndpoints(mux *chi.Mux) {
	mux.Get("/api/v0/groups/{group_id}/apps", a.handleGetAllowedAppsInGroup)
	mux.Post("/api/v0/groups/{group_id}/apps", a.handleAddAllowedAppToGroup)
	mux.Delete("/api/v0/groups/{group_id}/apps", a.handleRemoveAllowedAppsFromGroup)
	mux.Delete("/api/v0/groups/{group_id}/apps/{app}", a.handleRemoveAllowedAppFromGroup)

	mux.Get("/api/v0/apps/{app}/groups", a.handleGetAllowedGroupsForApp)
	mux.Delete("/api/v0/apps/{app}/groups", a.handleRemoveAllowedGroupsForApp)
}

func (a *API) handleGetAllowedAppsInGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "group_id")

	apps, err := a.service.GetAllowedApps(
		r.Context(),
		groupID,
	)
	if err != nil {
		rr := types.Response{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(rr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Data:    apps,
			Message: "List of allowed apps in group",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleAddAllowedAppToGroup(w http.ResponseWriter, r *http.Request) {
	var app App
	groupID := chi.URLParam(r, "group_id")
	defer r.Body.Close()

	err := json.NewDecoder(r.Body).Decode(&app)
	if err != nil {
		rr := types.Response{
			Status:  http.StatusBadRequest,
			Message: "Invalid request payload",
		}

		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(rr)

		return
	}

	err = a.service.AddAllowedApp(
		r.Context(),
		groupID,
		app.ClientID,
	)
	if err != nil {
		rr := types.Response{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(rr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Message: "App added to allowed list in group",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleRemoveAllowedAppsFromGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "group_id")

	err := a.service.RemoveAllowedApps(
		r.Context(),
		groupID,
	)
	if err != nil {
		rr := types.Response{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(rr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Message: "All apps removed from allowed list in group",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleRemoveAllowedAppFromGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "group_id")
	app := chi.URLParam(r, "app")

	err := a.service.RemoveAllowedApp(
		r.Context(),
		groupID,
		app,
	)
	if err != nil {
		rr := types.Response{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(rr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Message: "App removed from allowed list in group",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleGetAllowedGroupsForApp(w http.ResponseWriter, r *http.Request) {
	app := chi.URLParam(r, "app")

	groups, err := a.service.GetAllowedGroupsForApp(
		r.Context(),
		app,
	)

	if err != nil {
		rr := types.Response{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(rr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Data:    groups,
			Message: "List of groups allowed for app",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleRemoveAllowedGroupsForApp(w http.ResponseWriter, r *http.Request) {
	app := chi.URLParam(r, "app")

	err := a.service.RemoveAllowedGroupsForApp(
		r.Context(),
		app,
	)

	if err != nil {
		rr := types.Response{
			Status:  http.StatusInternalServerError,
			Message: err.Error(),
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(rr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Message: "All groups removed from allowed list for app",
			Status:  http.StatusOK,
		},
	)
}

func NewAPI(
	service ServiceInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *API {
	a := new(API)

	a.service = service

	a.monitor = monitor
	a.tracer = tracer
	a.logger = logger

	return a
}
