package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

// GetMemoriesHandler has two modes:
//   - empty `q` → List
//   - non-empty `q` → SearchText
//
// Both modes share the {items: [...]} response envelope.
func TestGetMemoriesHandler(t *testing.T) {
	testCases := []struct {
		name string

		queryString string // raw query string appended to /api/memories?

		listOut   []store.Memory
		searchOut []store.Memory
		readerErr error

		expectedStatus int
		expectContains string
		expectMode     string // "list" or "search" — which fake method should have been hit
		expectQuery    string // when search mode, what the handler forwarded as q
		expectLimit    int
	}{
		{
			name:           "no q → List, default limit 100",
			queryString:    "project_id=p1",
			listOut:        []store.Memory{{ID: 1, Content: "hi"}},
			expectedStatus: http.StatusOK,
			expectContains: `"items"`,
			expectMode:     "list",
			expectLimit:    100,
		},
		{
			name:           "q present → SearchText",
			queryString:    "project_id=p1&q=foo",
			searchOut:      []store.Memory{{ID: 2, Content: "foo result"}},
			expectedStatus: http.StatusOK,
			expectContains: `"foo result"`,
			expectMode:     "search",
			expectQuery:    "foo",
			expectLimit:    100,
		},
		{
			name:           "custom limit honoured",
			queryString:    "project_id=p1&limit=25",
			expectedStatus: http.StatusOK,
			expectMode:     "list",
			expectLimit:    25,
		},
		{
			name:           "reader error surfaces as 500",
			queryString:    "project_id=p1",
			readerErr:      assertErr("db down"),
			expectedStatus: http.StatusInternalServerError,
			expectMode:     "list",
			expectLimit:    100, // default still applied before reader returns
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			reader := &fakeMemoryReader{
				listOut:   tc.listOut,
				searchOut: tc.searchOut,
				err:       tc.readerErr,
			}
			handler := NewGetMemoriesHandler(GetMemoriesHandlerParams{Memory: reader})

			req := httptest.NewRequest(http.MethodGet, "/api/memories?"+tc.queryString, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			assert.Equal(tc.expectLimit, reader.gotLimit, "limit forwarded")
			if tc.expectMode == "search" {
				assert.Equal(tc.expectQuery, reader.gotQuery, "search query forwarded")
			} else {
				assert.Empty(reader.gotQuery, "list mode should not call SearchText")
			}
		})
	}
}
