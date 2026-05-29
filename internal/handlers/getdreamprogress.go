package handlers

import "net/http"

// GetDreamProgressHandler exposes the live state of the in-flight dream
// pass (or the last finished pass, when nothing is running). The UI
// polls this on the dashboard to render a "Dreaming…" indicator with
// session counters and the active session ID.
type GetDreamProgressHandler struct {
	svc DreamProgressService
}

type GetDreamProgressHandlerParams struct {
	Progress DreamProgressService
}

func NewGetDreamProgressHandler(params GetDreamProgressHandlerParams) *GetDreamProgressHandler {
	return &GetDreamProgressHandler{svc: params.Progress}
}

func (h *GetDreamProgressHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.svc.DreamProgress())
}
