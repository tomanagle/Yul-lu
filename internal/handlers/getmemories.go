package handlers

import "net/http"

// GetMemoriesHandler serves both list-recent and search modes from one
// endpoint, distinguished by the `q` query param. Empty q → newest-first
// list. Non-empty q → semantic vector search (the same retrieval the agent
// uses), so results carry similarity/rank + an injected flag and a
// natural-language query matches the way it does on the Retrievals page.
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
		memories, err := h.memory.SearchSemantic(r.Context(), projectID, query, limit)
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
