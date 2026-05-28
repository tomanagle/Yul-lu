package handlers

import (
	"encoding/json"
	"net/http"
)

type PostConfigHandler struct {
	cfg ConfigService
}

type PostConfigHandlerParams struct {
	Config ConfigService
}

func NewPostConfigHandler(params PostConfigHandlerParams) *PostConfigHandler {
	return &PostConfigHandler{cfg: params.Config}
}

func (h *PostConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var v ConfigView
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := h.cfg.SaveConfig(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status)
}
