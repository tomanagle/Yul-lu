package handlers

import (
	"encoding/json"
	"net/http"
)

// PostDreamPromptHandler persists a custom dream system prompt. Empty
// body content (or all-whitespace) deletes the override file and
// reverts to the built-in default — the same semantics the UI's
// "reset" button uses.
type PostDreamPromptHandler struct {
	svc DreamPromptService
}

type PostDreamPromptHandlerParams struct {
	Prompt DreamPromptService
}

func NewPostDreamPromptHandler(params PostDreamPromptHandlerParams) *PostDreamPromptHandler {
	return &PostDreamPromptHandler{svc: params.Prompt}
}

func (h *PostDreamPromptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	view, err := h.svc.SaveDreamPrompt(body.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, view)
}
