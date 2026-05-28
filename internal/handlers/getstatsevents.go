package handlers

import "net/http"

type GetStatsEventsHandler struct {
	stats MemoryStatsReader
}

type GetStatsEventsHandlerParams struct {
	Stats MemoryStatsReader
}

func NewGetStatsEventsHandler(params GetStatsEventsHandlerParams) *GetStatsEventsHandler {
	return &GetStatsEventsHandler{stats: params.Stats}
}

func (h *GetStatsEventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	events, err := h.stats.MemoryEventsByDay(r.Context(), q.Get("project_id"), atoiDefault(q.Get("days"), 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, events)
}
