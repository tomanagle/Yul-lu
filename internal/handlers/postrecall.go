package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/store"
)

// PostRecallHandler is the REST entry point for the UserPromptSubmit hook
// (`yullu inject`). Given the user's prompt text + caller cwd, it returns
// the top-K memories the agent should see before processing the prompt.
//
// Body shape:
//
//	{
//	  "query":      "the user's prompt verbatim",
//	  "cwd":        "/abs/path/where/the/user/is",
//	  "categories": ["process", "style"],   // optional; omit for unfiltered
//	  "limit":      5                        // optional; default 5
//	}
//
// Response shape:
//
//	{
//	  "project_id": "github.com/...",
//	  "results":    [<Memory>...]
//	}
//
// project_id is computed server-side from cwd (or a project_id override
// in the body if the caller already knows it). The hook treats the
// returned memories as ambient context to inject — no further filtering
// happens on the receiving side.
type PostRecallHandler struct {
	recall MemoryRecaller
}

type PostRecallHandlerParams struct {
	Recall MemoryRecaller
}

func NewPostRecallHandler(params PostRecallHandlerParams) *PostRecallHandler {
	return &PostRecallHandler{recall: params.Recall}
}

func (h *PostRecallHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query      string   `json:"query"`
		Cwd        string   `json:"cwd"`
		ProjectID  string   `json:"project_id"`
		Categories []string `json:"categories"`
		Limit      int      `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Query == "" {
		writeError(w, http.StatusBadRequest, errors.New("query is required"))
		return
	}

	projectID := body.ProjectID
	if projectID == "" && body.Cwd != "" {
		// Best-effort resolution from the caller's cwd. If we can't
		// resolve, fall through with empty project — Search returns
		// nothing for an empty id, which the hook formats as "no
		// memories" and moves on.
		if resolved, err := scope.Resolve(body.Cwd); err == nil {
			projectID = resolved
		}
	}

	cats := make([]store.MemoryCategory, 0, len(body.Categories))
	for _, c := range body.Categories {
		cats = append(cats, store.MemoryCategory(c))
	}

	results, err := h.recall.RecallMemories(r.Context(), projectID, body.Query, cats, body.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{
		"project_id": projectID,
		"results":    results,
	})
}
