package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/store"
)

func TestPutMemoryHandler(t *testing.T) {
	testCases := []struct {
		name string

		pathID string
		body   string

		editorOut *store.Memory
		editorErr error

		expectedStatus int
		expectContains string

		expectGotID      int64
		expectGotContent string
	}{
		{
			name:             "updates content + tags",
			pathID:           "7",
			body:             `{"content":"new","tags":["a","b"]}`,
			editorOut:        &store.Memory{ID: 7, Content: "new"},
			expectedStatus:   http.StatusOK,
			expectContains:   `"content":"new"`,
			expectGotID:      7,
			expectGotContent: "new",
		},
		{
			name:           "non-numeric id is 400",
			pathID:         "abc",
			body:           `{"content":"x"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed body is 400",
			pathID:         "1",
			body:           `{not json`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "editor error surfaces as 500",
			pathID:         "1",
			body:           `{"content":"x"}`,
			editorErr:      assertErr("update failed"),
			expectedStatus: http.StatusInternalServerError,
			expectContains: "update failed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			editor := &fakeMemoryEditor{updateOut: tc.editorOut, updateErr: tc.editorErr}
			handler := NewPutMemoryHandler(PutMemoryHandlerParams{Editor: editor})

			req := httptest.NewRequest(http.MethodPut, "/api/memories/"+tc.pathID, strings.NewReader(tc.body))
			req.SetPathValue("id", tc.pathID)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code, "status code")
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			if tc.expectGotID != 0 {
				assert.Equal(tc.expectGotID, editor.updateGotID, "editor saw id")
				assert.Equal(tc.expectGotContent, editor.updateGotContent, "editor saw content")
			}
		})
	}
}
