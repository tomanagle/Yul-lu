package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/tomanagle/yullu/internal/server"
)

type PostDreamHandler struct {
	dreamer Dreamer
	// contextMemories is a thunk that returns the current [dreaming]
	// .context_memories value. Re-evaluated on every request so a
	// SaveConfig that changes the limit takes effect immediately —
	// previously this captured the value at construction time and went
	// stale until the routes were rebuilt.
	contextMemories func() int
}

type PostDreamHandlerParams struct {
	Dreamer         Dreamer
	ContextMemories func() int
}

func NewPostDreamHandler(params PostDreamHandlerParams) *PostDreamHandler {
	return &PostDreamHandler{
		dreamer:         params.Dreamer,
		contextMemories: params.ContextMemories,
	}
}

func (h *PostDreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
	}
	// Body is optional - empty body means use the CWD's project.
	_ = json.NewDecoder(r.Body).Decode(&body)
	result, err := h.dreamer.Dream(r.Context(), server.DreamOptions{
		ProjectID:       body.ProjectID,
		ContextMemories: h.contextMemories(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, result)
}
