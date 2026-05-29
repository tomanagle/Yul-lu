package handlers

import "net/http"

// GetDreamPromptHandler returns the current dream system prompt plus
// the compiled-in default so the UI can offer "reset to default" and
// show what the user diverged from.
type GetDreamPromptHandler struct {
	svc DreamPromptService
}

type GetDreamPromptHandlerParams struct {
	Prompt DreamPromptService
}

func NewGetDreamPromptHandler(params GetDreamPromptHandlerParams) *GetDreamPromptHandler {
	return &GetDreamPromptHandler{svc: params.Prompt}
}

func (h *GetDreamPromptHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.svc.GetDreamPrompt())
}
