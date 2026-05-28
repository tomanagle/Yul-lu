package handlers

import "net/http"

type GetStatsHandler struct {
	stats MemoryStatsReader
}

type GetStatsHandlerParams struct {
	Stats MemoryStatsReader
}

func NewGetStatsHandler(params GetStatsHandlerParams) *GetStatsHandler {
	return &GetStatsHandler{stats: params.Stats}
}

func (h *GetStatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	stats, err := h.stats.GetMemoryStats(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, stats)
}
