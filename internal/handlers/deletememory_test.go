package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeleteMemoryHandler(t *testing.T) {
	testCases := []struct {
		name string

		pathID    string
		editorErr error

		expectedStatus int
		expectGotID    int64 // 0 = "don't check, handler bailed before calling"
	}{
		{
			name:           "deletes and returns 204",
			pathID:         "9",
			expectedStatus: http.StatusNoContent,
			expectGotID:    9,
		},
		{
			name:           "non-numeric id is 400",
			pathID:         "abc",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "editor error surfaces as 500",
			pathID:         "1",
			editorErr:      assertErr("FK constraint"),
			expectedStatus: http.StatusInternalServerError,
			expectGotID:    1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			editor := &fakeMemoryEditor{deleteErr: tc.editorErr}
			handler := NewDeleteMemoryHandler(DeleteMemoryHandlerParams{Editor: editor})

			req := httptest.NewRequest(http.MethodDelete, "/api/memories/"+tc.pathID, nil)
			req.SetPathValue("id", tc.pathID)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			if tc.expectGotID != 0 {
				assert.Equal(tc.expectGotID, editor.deleteGotID)
			}
		})
	}
}
