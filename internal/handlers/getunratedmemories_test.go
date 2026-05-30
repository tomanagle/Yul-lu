package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

func TestGetUnratedMemoriesHandler(t *testing.T) {
	testCases := []struct {
		name string

		queryString string

		listOut []store.Memory
		listErr error

		expectedStatus int
		expectContains string
	}{
		{
			name:           "returns unrated memories",
			queryString:    "project_id=p1",
			listOut:        []store.Memory{{ID: 1, Content: "needs review"}},
			expectedStatus: http.StatusOK,
			expectContains: `"needs review"`,
		},
		{
			name:           "empty list returns empty envelope",
			queryString:    "project_id=p1",
			expectedStatus: http.StatusOK,
			expectContains: `"items"`,
		},
		{
			name:           "rater error surfaces as 500",
			queryString:    "project_id=p1",
			listErr:        assertErr("db locked"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			rater := &fakeRater{listOut: tc.listOut, listErr: tc.listErr}
			handler := NewGetUnratedMemoriesHandler(GetUnratedMemoriesHandlerParams{Rater: rater})

			req := httptest.NewRequest(http.MethodGet, "/api/memories/unrated?"+tc.queryString, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
		})
	}
}
