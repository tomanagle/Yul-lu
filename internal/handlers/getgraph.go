package handlers

import "net/http"

type GetGraphHandler struct {
	graph GraphReader
}

type GetGraphHandlerParams struct {
	Graph GraphReader
}

func NewGetGraphHandler(params GetGraphHandlerParams) *GetGraphHandler {
	return &GetGraphHandler{graph: params.Graph}
}

func (h *GetGraphHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	graph, err := h.graph.MemoryGraph(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, graph)
}
