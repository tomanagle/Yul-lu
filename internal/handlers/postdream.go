package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/tomanagle/yullu/internal/server"
)

type PostDreamHandler struct {
	dreamer Dreamer
	// contextMemories is the number of recent memories the reasoner sees per
	// pass. Stored on the handler so it tracks the user's [dreaming] config
	// at construction time; if SaveConfig changes it, the handler is rebuilt.
	contextMemories int
}

type PostDreamHandlerParams struct {
	Dreamer         Dreamer
	ContextMemories int
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
		ContextMemories: h.contextMemories,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, result)
}
