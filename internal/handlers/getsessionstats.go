package handlers

import "net/http"

type GetSessionStatsHandler struct {
	session SessionStatsProvider
}

type GetSessionStatsHandlerParams struct {
	Session SessionStatsProvider
}

func NewGetSessionStatsHandler(params GetSessionStatsHandlerParams) *GetSessionStatsHandler {
	return &GetSessionStatsHandler{session: params.Session}
}

func (h *GetSessionStatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	stats, err := h.session.GetSessionStats(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, stats)
}
