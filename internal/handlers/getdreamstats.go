package handlers

import "net/http"

// GetDreamStatsHandler serves aggregated dream-pass telemetry. Window is
// `days` (default 30); projectID is the optional scope filter.
type GetDreamStatsHandler struct {
	stats DreamStatsReader
}

type GetDreamStatsHandlerParams struct {
	Stats DreamStatsReader
}

func NewGetDreamStatsHandler(params GetDreamStatsHandlerParams) *GetDreamStatsHandler {
	return &GetDreamStatsHandler{stats: params.Stats}
}

func (h *GetDreamStatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ds, err := h.stats.DreamStats(r.Context(), q.Get("project_id"), atoiDefault(q.Get("days"), 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, ds)
}
