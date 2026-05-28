package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
)

var errBadProjectID = errors.New("project_id path segment is required")

// PostProjectOverridesHandler writes both layers of a project's overrides.
// Body shape mirrors the GET response (without `effective` / `warnings`):
//
//	{ "project_id": "...", "repo": { ... }, "user": { ... } }
//
// The service enforces the secret-routing rule (api keys may only land in
// the user layer); attempts to put them in `repo` get stripped and surfaced
// via the returned warnings.
type PostProjectOverridesHandler struct {
	svc ProjectOverridesService
}

type PostProjectOverridesHandlerParams struct {
	Overrides ProjectOverridesService
}

func NewPostProjectOverridesHandler(params PostProjectOverridesHandlerParams) *PostProjectOverridesHandler {
	return &PostProjectOverridesHandler{svc: params.Overrides}
}

func (h *PostProjectOverridesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, errBadProjectID)
		return
	}
	var body struct {
		Repo ProjectOverridePayload `json:"repo"`
		User ProjectOverridePayload `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	view, err := h.svc.SaveProjectOverrides(r.Context(), projectID, body.Repo, body.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, view)
}
