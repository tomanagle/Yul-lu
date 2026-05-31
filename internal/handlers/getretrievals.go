package handlers

import "net/http"

// GetRetrievalsHandler powers the Retrievals review surface: recent recall
// events for the current project (newest first), each joined to the memory
// it returned plus the query, similarity, rank, and any verdict the
// developer has already left. This is the "see WHY a memory was retrieved"
// view that makes a relevance verdict meaningful.
type GetRetrievalsHandler struct {
	retrievals RetrievalAnalytics
}

type GetRetrievalsHandlerParams struct {
	Retrievals RetrievalAnalytics
}

func NewGetRetrievalsHandler(params GetRetrievalsHandlerParams) *GetRetrievalsHandler {
	return &GetRetrievalsHandler{retrievals: params.Retrievals}
}

func (h *GetRetrievalsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project_id")
	limit := atoiDefault(q.Get("limit"), 100)
	events, err := h.retrievals.ListRetrievals(r.Context(), projectID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, events)
}
