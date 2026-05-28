package handlers

import (
	"net/http"
	"strconv"
)

type DeleteMemoryHandler struct {
	editor MemoryEditor
}

type DeleteMemoryHandlerParams struct {
	Editor MemoryEditor
}

func NewDeleteMemoryHandler(params DeleteMemoryHandlerParams) *DeleteMemoryHandler {
	return &DeleteMemoryHandler{editor: params.Editor}
}

func (h *DeleteMemoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.editor.DeleteMemory(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
