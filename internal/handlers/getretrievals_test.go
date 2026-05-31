package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

func TestGetRetrievalsHandler(t *testing.T) {
	testCases := []struct {
		name string

		queryString string

		listOut []store.RetrievalEvent
		listErr error

		expectedStatus   int
		expectContains   string
		expectGotProject string
		expectGotLimit   int
	}{
		{
			name:             "returns retrievals for the project",
			queryString:      "project_id=p1&limit=50",
			listOut:          []store.RetrievalEvent{{EventID: 1, Query: "how does auth work", MemoryContent: "auth note"}},
			expectedStatus:   http.StatusOK,
			expectContains:   `"how does auth work"`,
			expectGotProject: "p1",
			expectGotLimit:   50,
		},
		{
			name:             "empty list returns empty envelope",
			queryString:      "project_id=p1",
			expectedStatus:   http.StatusOK,
			expectContains:   `"items"`,
			expectGotProject: "p1",
			expectGotLimit:   100, // default applied by atoiDefault
		},
		{
			name:           "store error surfaces as 500",
			queryString:    "project_id=p1",
			listErr:        assertErr("db locked"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			fake := &fakeRetrievalAnalytics{listOut: tc.listOut, listErr: tc.listErr}
			handler := NewGetRetrievalsHandler(GetRetrievalsHandlerParams{Retrievals: fake})

			req := httptest.NewRequest(http.MethodGet, "/api/retrievals?"+tc.queryString, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			if tc.expectGotProject != "" {
				assert.Equal(tc.expectGotProject, fake.gotListProjectID, "store saw project_id")
				assert.Equal(tc.expectGotLimit, fake.gotListLimit, "store saw limit")
			}
		})
	}
}
