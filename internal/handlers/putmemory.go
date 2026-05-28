package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type PutMemoryHandler struct {
	editor MemoryEditor
}

type PutMemoryHandlerParams struct {
	Editor MemoryEditor
}

func NewPutMemoryHandler(params PutMemoryHandlerParams) *PutMemoryHandler {
	return &PutMemoryHandler{editor: params.Editor}
}

func (h *PutMemoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	mem, err := h.editor.UpdateMemory(r.Context(), id, body.Content, body.Tags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, mem)
}
