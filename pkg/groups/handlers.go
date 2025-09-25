package groups

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/hook-service/internal/http/types"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/go-chi/chi/v5"
)

type API struct {
	service ServiceInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

func (a *API) RegisterEndpoints(mux *chi.Mux) {
	mux.Get("/api/v0/groups", a.handleGetGroups)
	mux.Get("/api/v0/groups/{group_id}", a.handleGetGroup)
	mux.Post("/api/v0/groups", a.handleCreateGroup)
	mux.Delete("/api/v0/groups/{group_id}", a.handleDeleteGroup)
	mux.Get("/api/v0/groups/{group_id}/users", a.handleGetGroupMembers)
	mux.Post("/api/v0/groups/{group_id}/users", a.handleAddGroupMember)
	mux.Delete("/api/v0/groups/{group_id}/users/{user_id}", a.handleRemoveGroupMember)

	mux.Get("/api/v0/users/{user_id}/groups", a.handleGetUserGroups)
	mux.Put("/api/v0/users/{user_id}/groups", a.handleAddUserToGroups)
}

func (a *API) handleGetGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	groups, err := a.service.ListGroups(
		r.Context(),
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

	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Data:    groups,
			Message: "List of groups",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	groupID := chi.URLParam(r, "group_id")

	group, err := a.service.GetGroup(
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

	if group == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(types.Response{
			Status:  http.StatusNotFound,
			Message: "Group not found",
		})
		return
	}

	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Data:    []Group{*group},
			Message: "Group details",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	defer r.Body.Close()

	var group Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		rr := types.Response{
			Status:  http.StatusBadRequest,
			Message: "Invalid request body",
		}

		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(rr)

		return
	}

	if group.ID != "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(
			types.Response{
				Message: "Group ID field is not allowed to be passed in",
				Status:  http.StatusBadRequest,
			},
		)

		return
	}

	g, err := a.service.CreateGroup(
		r.Context(),
		group.Name,
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

	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(
		types.Response{
			Data:    []Group{*g},
			Message: fmt.Sprintf("Created group %s", g.Name),
			Status:  http.StatusCreated,
		},
	)
}

func (a *API) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	groupID := chi.URLParam(r, "group_id")

	err := a.service.DeleteGroup(
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

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleGetGroupMembers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	groupID := chi.URLParam(r, "group_id")

	users, err := a.service.ListGroupMembers(
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

	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Data:    users,
			Message: "List of group members",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	defer r.Body.Close()

	ID := chi.URLParam(r, "group_id")
	var user User

	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		rr := types.Response{
			Status:  http.StatusBadRequest,
			Message: "Invalid request body",
		}

		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(rr)

		return
	}

	err := a.service.AddGroupMember(
		r.Context(),
		ID,
		user.UserID,
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

	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(
		types.Response{
			Message: fmt.Sprintf("Added user %s to group %s", user.UserID, ID),
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	groupID := chi.URLParam(r, "group_id")
	userID := chi.URLParam(r, "user_id")

	err := a.service.RemoveGroupMember(
		r.Context(),
		groupID,
		userID,
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

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(
		types.Response{
			Message: fmt.Sprintf("Removed user %s from group %s", userID, groupID),
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleGetUserGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := chi.URLParam(r, "user_id")

	groups, err := a.service.ListUserGroups(
		r.Context(),
		userID,
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

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(
		types.Response{
			Data:    groups,
			Message: "List of user groups",
			Status:  http.StatusOK,
		},
	)
}

func (a *API) handleAddUserToGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := chi.URLParam(r, "user_id")

	var groups []Group

	if err := json.NewDecoder(r.Body).Decode(&groups); err != nil {
		rr := types.Response{
			Status:  http.StatusBadRequest,
			Message: "Invalid request body",
		}

		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(rr)

		return
	}

	for _, group := range groups {
		if group.ID == "" {
			rr := types.Response{
				Status:  http.StatusBadRequest,
				Message: "Group ID field is required",
			}

			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(rr)

			return
		}

		if err := a.service.AddGroupMember(
			r.Context(),
			group.ID,
			userID,
		); err != nil {
			rr := types.Response{
				Status:  http.StatusInternalServerError,
				Message: err.Error(),
			}

			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(rr)

			return
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(
		types.Response{
			Message: fmt.Sprintf("Added user %s to groups", userID),
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
