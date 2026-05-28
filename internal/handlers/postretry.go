package handlers

import "net/http"

type PostRetryHandler struct {
	status StatusService
}

type PostRetryHandlerParams struct {
	Status StatusService
}

func NewPostRetryHandler(params PostRetryHandlerParams) *PostRetryHandler {
	return &PostRetryHandler{status: params.Status}
}

func (h *PostRetryHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.status.Retry())
}
