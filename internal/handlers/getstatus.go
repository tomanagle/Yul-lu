package handlers

import "net/http"

type GetStatusHandler struct {
	status StatusService
}

type GetStatusHandlerParams struct {
	Status StatusService
}

func NewGetStatusHandler(params GetStatusHandlerParams) *GetStatusHandler {
	return &GetStatusHandler{status: params.Status}
}

func (h *GetStatusHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.status.Status())
}
