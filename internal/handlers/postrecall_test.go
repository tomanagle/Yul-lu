package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

func TestPostRecallHandler(t *testing.T) {
	testCases := []struct {
		name string

		body string

		recallOut []store.Memory
		recallErr error

		expectedStatus    int
		expectContains    string
		expectGotProject  string // when set, asserts recaller saw this project_id
		expectGotCats     int    // number of categories forwarded; -1 = skip
		expectGotLimit    int
		expectGotQuery    string
	}{
		{
			name:           "happy path with explicit project_id and categories",
			body:           `{"query":"how to deploy","project_id":"p1","categories":["process","gotcha"],"limit":3}`,
			recallOut:      []store.Memory{{ID: 1, Content: "deploy via fly"}},
			expectedStatus: http.StatusOK,
			expectContains: `"deploy via fly"`,
			expectGotProject: "p1",
			expectGotCats:    2,
			expectGotLimit:   3,
			expectGotQuery:   "how to deploy",
		},
		{
			name:           "missing query is 400",
			body:           `{"cwd":"/repo"}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "query is required",
			expectGotCats:  -1,
		},
		{
			name:           "malformed body is 400",
			body:           `{not json`,
			expectedStatus: http.StatusBadRequest,
			expectGotCats:  -1,
		},
		{
			name:           "recaller error surfaces as 500",
			body:           `{"query":"x","project_id":"p1"}`,
			recallErr:      assertErr("embed fail"),
			expectedStatus: http.StatusInternalServerError,
			expectContains: "embed fail",
			expectGotCats:  0,
		},
		{
			name:           "empty results return empty list",
			body:           `{"query":"x","project_id":"p1"}`,
			recallOut:      nil,
			expectedStatus: http.StatusOK,
			expectContains: `"results"`,
			expectGotCats:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			recall := &fakeMemoryRecaller{recallOut: tc.recallOut, recallErr: tc.recallErr}
			handler := NewPostRecallHandler(PostRecallHandlerParams{Recall: recall})

			req := httptest.NewRequest(http.MethodPost, "/api/memories/recall", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			if tc.expectGotProject != "" {
				assert.Equal(tc.expectGotProject, recall.gotProjectID)
				assert.Equal(tc.expectGotQuery, recall.gotQuery)
				assert.Equal(tc.expectGotLimit, recall.gotLimit)
			}
			if tc.expectGotCats >= 0 {
				assert.Len(recall.gotCategories, tc.expectGotCats)
			}
		})
	}
}
