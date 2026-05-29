package handlers

import "net/http"

// GetSessionsHandler returns every buffered (un-dreamed) session for
// a project, each with its full message list. Used by the Dreaming
// page's "Buffered sessions" card so the user can see exactly what
// the next dream pass will chew on.
type GetSessionsHandler struct {
	buffer SessionBufferReader
}

type GetSessionsHandlerParams struct {
	Buffer SessionBufferReader
}

func NewGetSessionsHandler(params GetSessionsHandlerParams) *GetSessionsHandler {
	return &GetSessionsHandler{buffer: params.Buffer}
}

func (h *GetSessionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	sessions, err := h.buffer.BufferedSessions(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, sessions)
}
