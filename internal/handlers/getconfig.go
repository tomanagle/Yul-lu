package handlers

import "net/http"

type GetConfigHandler struct {
	cfg ConfigService
}

type GetConfigHandlerParams struct {
	Config ConfigService
}

func NewGetConfigHandler(params GetConfigHandlerParams) *GetConfigHandler {
	return &GetConfigHandler{cfg: params.Config}
}

func (h *GetConfigHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.cfg.GetConfig())
}
