package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
)

var errSessionRequired = errors.New("session_id is required")

// PostMessagesHandler is the REST entry point that mirrors the
// `record_messages` MCP tool. Used by the Claude Code Stop hook (via
// `yullu record-turn`) so hooks don't need to speak MCP JSON-RPC to feed
// the dream buffer. Body shape:
//
//	{
//	  "session_id": "string",
//	  "project_id": "optional override",
//	  "messages": [{ "role": "user" | "assistant", "content": "..." }]
//	}
//
// Response echoes the resolved project_id and the count of messages
// appended. Empty/trivial requests are accepted as 204 No Content (no
// rows written) — callers are expected to filter, but tolerance at the
// server side keeps hooks simple.
type PostMessagesHandler struct {
	recorder MessageRecorder
}

type PostMessagesHandlerParams struct {
	Recorder MessageRecorder
}

func NewPostMessagesHandler(params PostMessagesHandlerParams) *PostMessagesHandler {
	return &PostMessagesHandler{recorder: params.Recorder}
}

func (h *PostMessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string            `json:"session_id"`
		ProjectID string            `json:"project_id"`
		Messages  []RecordedMessage `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.SessionID == "" {
		writeError(w, http.StatusBadRequest, errSessionRequired)
		return
	}
	if len(body.Messages) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	projectID, err := h.recorder.RecordMessages(
		r.Context(), body.ProjectID, body.SessionID, body.Messages,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{
		"session_id": body.SessionID,
		"project_id": projectID,
		"count":      len(body.Messages),
	})
}
