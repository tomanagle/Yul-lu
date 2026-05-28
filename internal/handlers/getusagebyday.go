package handlers

import "net/http"

type GetUsageByDayHandler struct {
	usage UsageReader
}

type GetUsageByDayHandlerParams struct {
	Usage UsageReader
}

func NewGetUsageByDayHandler(params GetUsageByDayHandlerParams) *GetUsageByDayHandler {
	return &GetUsageByDayHandler{usage: params.Usage}
}

func (h *GetUsageByDayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	usage, err := h.usage.UsageByDay(r.Context(), atoiDefault(r.URL.Query().Get("days"), 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, usage)
}
