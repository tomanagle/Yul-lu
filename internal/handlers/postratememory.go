package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

var errInvalidRating = errors.New("rating must be an integer between 1 and 10")

// PostRateMemoryHandler accepts a user rating for a memory. Two branches:
//
//   - rating ≥ 6 → the memory survives. The response is the updated row
//     (so the FE can refresh inline without a second fetch).
//   - rating ≤ 5 → the memory is moved to the rejected-memories table
//     and removed from `memories`. Response shape echoes {rejected: true}
//     with no memory body, so the FE knows to drop the row from the list.
//
// Body shape: { "rating": 1..10, "comment": "..." }
type PostRateMemoryHandler struct {
	rater MemoryRater
}

type PostRateMemoryHandlerParams struct {
	Rater MemoryRater
}

func NewPostRateMemoryHandler(params PostRateMemoryHandlerParams) *PostRateMemoryHandler {
	return &PostRateMemoryHandler{rater: params.Rater}
}

func (h *PostRateMemoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid memory id"))
		return
	}
	var body struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Rating < 1 || body.Rating > 10 {
		writeError(w, http.StatusBadRequest, errInvalidRating)
		return
	}
	mem, err := h.rater.RateMemory(r.Context(), id, body.Rating, body.Comment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if mem == nil {
		// Rating ≤ 5 → memory was moved to rejected_memories.
		writeJSON(w, map[string]any{"rejected": true, "id": id, "rating": body.Rating})
		return
	}
	writeJSON(w, mem)
}
