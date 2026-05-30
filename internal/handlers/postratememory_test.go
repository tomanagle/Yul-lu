package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

// Table-driven test for PostRateMemoryHandler.
//
// To add a new case: append a row to testCases. Each row describes the
// id in the URL, the JSON body, the fake rater's configured behaviour,
// and what the response should look like. The loop body never needs to
// change — branch coverage is purely additive.
func TestPostRateMemoryHandler(t *testing.T) {
	memID := int64(42)
	survivingMemory := &store.Memory{ID: memID, Content: "kept", Rating: ptrInt(8)}

	testCases := []struct {
		name string

		// Input shape.
		pathID string // empty means "don't set" — caught by ParseInt
		body   string // raw JSON; use "" for an unparseable empty body

		// Fake-rater behaviour.
		raterOut *store.Memory
		raterErr error

		// Expected response.
		expectedStatus  int
		expectRejected  bool
		expectContains  string
		expectRaterID   int64 // 0 means "don't check" (handler bailed before calling)
		expectRaterRate int
	}{
		{
			name:           "rating 8 keeps the memory",
			pathID:         "42",
			body:           `{"rating":8,"comment":"useful"}`,
			raterOut:       survivingMemory,
			expectedStatus: http.StatusOK,
			expectContains: `"content":"kept"`,
			expectRaterID:  memID,
			expectRaterRate: 8,
		},
		{
			name:            "rating 3 archives as rejected",
			pathID:          "42",
			body:            `{"rating":3,"comment":"too vague"}`,
			raterOut:        nil, // RateMemory returns nil to signal rejection
			expectedStatus:  http.StatusOK,
			expectRejected:  true,
			expectRaterID:   memID,
			expectRaterRate: 3,
		},
		{
			name:           "non-numeric id rejected before rater is called",
			pathID:         "not-a-number",
			body:           `{"rating":8}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "invalid memory id",
		},
		{
			name:           "malformed body rejected",
			pathID:         "42",
			body:           `{not json`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "rating 0 fails validation",
			pathID:         "42",
			body:           `{"rating":0}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "rating must be an integer between 1 and 10",
		},
		{
			name:           "rating 11 fails validation",
			pathID:         "42",
			body:           `{"rating":11}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "rating must be an integer between 1 and 10",
		},
		{
			name:           "rater error surfaces as 500",
			pathID:         "42",
			body:           `{"rating":7}`,
			raterErr:       errors.New("db locked"),
			expectedStatus: http.StatusInternalServerError,
			expectContains: "db locked",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			rater := &fakeRater{rateOut: tc.raterOut, rateErr: tc.raterErr}
			handler := NewPostRateMemoryHandler(PostRateMemoryHandlerParams{Rater: rater})

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/memories/"+tc.pathID+"/rate",
				strings.NewReader(tc.body),
			)
			// PathValue is populated by net/http's pattern matcher in
			// real use. httptest doesn't run the mux, so we set it by
			// hand.
			req.SetPathValue("id", tc.pathID)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code, "status code")

			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}

			if tc.expectRejected {
				var got map[string]any
				assert.NoError(json.NewDecoder(rr.Body).Decode(&got))
				assert.Equal(true, got["rejected"], "rejected flag")
			}

			// Verify the handler forwarded inputs to the rater for the
			// success branches. Skipped when the handler bailed early.
			if tc.expectRaterID != 0 {
				assert.Equal(tc.expectRaterID, rater.rateGotID, "rater saw id")
				assert.Equal(tc.expectRaterRate, rater.rateGotRating, "rater saw rating")
			}
		})
	}
}

func ptrInt(i int) *int { return &i }
