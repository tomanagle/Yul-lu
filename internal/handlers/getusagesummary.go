package handlers

import (
	"net/http"
	"time"
)

type GetUsageSummaryHandler struct {
	usage UsageReader
}

type GetUsageSummaryHandlerParams struct {
	Usage UsageReader
}

func NewGetUsageSummaryHandler(params GetUsageSummaryHandlerParams) *GetUsageSummaryHandler {
	return &GetUsageSummaryHandler{usage: params.Usage}
}

func (h *GetUsageSummaryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sinceHours := atoiDefault(r.URL.Query().Get("since_hours"), 0)
	var since time.Time
	if sinceHours > 0 {
		since = time.Now().Add(-time.Duration(sinceHours) * time.Hour)
	}
	summary, err := h.usage.UsageSummary(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, summary)
}
