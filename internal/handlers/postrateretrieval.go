package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/tomanagle/yullu/internal/store"
)

var errInvalidVerdict = errors.New("verdict must be 1 (good match) or -1 (bad match)")

// PostRateRetrievalHandler records a developer's relevance verdict on a
// single recall, keyed to the recall event id (not the memory). A verdict
// of +1 marks "good match for that query", -1 marks "shouldn't have
// surfaced". Re-rating the same event overwrites the prior verdict.
//
// Body shape: { "verdict": 1 | -1, "comment": "..." }
type PostRateRetrievalHandler struct {
	retrievals RetrievalAnalytics
}

type PostRateRetrievalHandlerParams struct {
	Retrievals RetrievalAnalytics
}

func NewPostRateRetrievalHandler(params PostRateRetrievalHandlerParams) *PostRateRetrievalHandler {
	return &PostRateRetrievalHandler{retrievals: params.Retrievals}
}

func (h *PostRateRetrievalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(r.PathValue("eventID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid event id"))
		return
	}
	var body struct {
		Verdict int    `json:"verdict"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Verdict != 1 && body.Verdict != -1 {
		writeError(w, http.StatusBadRequest, errInvalidVerdict)
		return
	}
	if err := h.retrievals.RateRetrieval(r.Context(), eventID, body.Verdict, body.Comment); err != nil {
		// A rating for an unknown / non-recall event is a client error, not
		// a server fault — surface it as a 404 so the FE can drop the row.
		if errors.Is(err, store.ErrNotRecallEvent) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"event_id": eventID, "verdict": body.Verdict})
}
