package handlers

import "net/http"

type GetProjectsHandler struct {
	projects ProjectLister
}

type GetProjectsHandlerParams struct {
	Projects ProjectLister
}

func NewGetProjectsHandler(params GetProjectsHandlerParams) *GetProjectsHandler {
	return &GetProjectsHandler{projects: params.Projects}
}

func (h *GetProjectsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	projects, err := h.projects.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeList(w, projects)
}
