package handlers

import "net/http"

// GetMemoriesHandler serves both list-recent and search modes from one
// endpoint, distinguished by the `q` query param. Empty q → newest-first
// list. Non-empty q → BM25 full-text search.
type GetMemoriesHandler struct {
	memory MemoryReader
}

type GetMemoriesHandlerParams struct {
	Memory MemoryReader
}

func NewGetMemoriesHandler(params GetMemoriesHandlerParams) *GetMemoriesHandler {
	return &GetMemoriesHandler{memory: params.Memory}
}

func (h *GetMemoriesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project_id")
	limit := atoiDefault(q.Get("limit"), 100)
	query := q.Get("q")

	if query != "" {
		memories, err := h.memory.SearchText(r.Context(), projectID, query, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeList(w, memories)
		return
	}
	memories, err := h.memory.List(r.Context(), projectID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, memories)
}
