package handlers

import "net/http"

// GetDreamPassesHandler returns the per-cycle dream history for the
// Stats page. Each row is a non-skipped pass with its counters + any
// errors. Newest first. project_id query param scopes; absent = all
// projects.
type GetDreamPassesHandler struct {
	lister DreamPassLister
}

type GetDreamPassesHandlerParams struct {
	Lister DreamPassLister
}

func NewGetDreamPassesHandler(params GetDreamPassesHandlerParams) *GetDreamPassesHandler {
	return &GetDreamPassesHandler{lister: params.Lister}
}

func (h *GetDreamPassesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project_id")
	limit := atoiDefault(q.Get("limit"), 30)
	passes, err := h.lister.ListDreamPasses(r.Context(), projectID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, passes)
}
