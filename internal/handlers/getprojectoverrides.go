package handlers

import "net/http"

// GetProjectOverridesHandler returns the per-project override layers + the
// resolved effective config for the {id} path segment. Project IDs contain
// slashes ("github.com/owner/repo") so callers URL-encode them.
type GetProjectOverridesHandler struct {
	svc ProjectOverridesService
}

type GetProjectOverridesHandlerParams struct {
	Overrides ProjectOverridesService
}

func NewGetProjectOverridesHandler(params GetProjectOverridesHandlerParams) *GetProjectOverridesHandler {
	return &GetProjectOverridesHandler{svc: params.Overrides}
}

func (h *GetProjectOverridesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, errBadProjectID)
		return
	}
	view, err := h.svc.GetProjectOverrides(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, view)
}
