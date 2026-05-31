package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

// Table-driven test for PostRateRetrievalHandler.
//
// To add a case: append a row. Each describes the eventID in the URL, the
// JSON body, the fake's configured error, and the expected response. The
// loop body never changes — coverage is additive.
func TestPostRateRetrievalHandler(t *testing.T) {
	testCases := []struct {
		name string

		pathID string
		body   string

		rateErr error

		expectedStatus  int
		expectContains  string
		expectEventID   int64 // 0 = handler bailed before calling the fake
		expectVerdict   int
		expectComment   string
	}{
		{
			name:           "thumbs up is recorded",
			pathID:         "7",
			body:           `{"verdict":1,"comment":"spot on"}`,
			expectedStatus: http.StatusOK,
			expectContains: `"verdict":1`,
			expectEventID:  7,
			expectVerdict:  1,
			expectComment:  "spot on",
		},
		{
			name:           "thumbs down is recorded",
			pathID:         "7",
			body:           `{"verdict":-1}`,
			expectedStatus: http.StatusOK,
			expectContains: `"event_id":7`,
			expectEventID:  7,
			expectVerdict:  -1,
		},
		{
			name:           "non-numeric event id rejected before the store is touched",
			pathID:         "nope",
			body:           `{"verdict":1}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "invalid event id",
		},
		{
			name:           "malformed body rejected",
			pathID:         "7",
			body:           `{not json`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "verdict 0 fails validation",
			pathID:         "7",
			body:           `{"verdict":0}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "verdict must be 1",
		},
		{
			name:           "verdict 2 fails validation",
			pathID:         "7",
			body:           `{"verdict":2}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "verdict must be 1",
		},
		{
			name:           "unknown recall event surfaces as 404",
			pathID:         "7",
			body:           `{"verdict":1}`,
			rateErr:        store.ErrNotRecallEvent,
			expectedStatus: http.StatusNotFound,
			expectContains: "not a recall event",
			expectEventID:  7,
			expectVerdict:  1,
		},
		{
			name:           "store error surfaces as 500",
			pathID:         "7",
			body:           `{"verdict":1}`,
			rateErr:        assertErr("db locked"),
			expectedStatus: http.StatusInternalServerError,
			expectContains: "db locked",
			expectEventID:  7,
			expectVerdict:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			fake := &fakeRetrievalAnalytics{rateErr: tc.rateErr}
			handler := NewPostRateRetrievalHandler(PostRateRetrievalHandlerParams{Retrievals: fake})

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/retrievals/"+tc.pathID+"/rate",
				strings.NewReader(tc.body),
			)
			req.SetPathValue("eventID", tc.pathID)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code, "status code")
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			if tc.expectEventID != 0 {
				assert.Equal(tc.expectEventID, fake.gotRateEventID, "store saw event id")
				assert.Equal(tc.expectVerdict, fake.gotRateVerdict, "store saw verdict")
				assert.Equal(tc.expectComment, fake.gotRateComment, "store saw comment")
			}
		})
	}
}
