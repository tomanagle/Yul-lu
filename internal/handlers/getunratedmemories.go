package handlers

import "net/http"

// GetUnratedMemoriesHandler powers the dedicated Review queue. Returns
// memories for the current project that the user hasn't scored yet,
// newest first. Memories with a rating already set (i.e. survived the
// review at ≥ 6) drop out of the list; memories rated ≤ 5 are gone from
// the `memories` table entirely (moved to rejected_memories).
type GetUnratedMemoriesHandler struct {
	rater MemoryRater
}

type GetUnratedMemoriesHandlerParams struct {
	Rater MemoryRater
}

func NewGetUnratedMemoriesHandler(params GetUnratedMemoriesHandlerParams) *GetUnratedMemoriesHandler {
	return &GetUnratedMemoriesHandler{rater: params.Rater}
}

func (h *GetUnratedMemoriesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project_id")
	limit := atoiDefault(q.Get("limit"), 100)
	memories, err := h.rater.ListUnrated(r.Context(), projectID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, memories)
}
