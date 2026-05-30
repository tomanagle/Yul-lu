package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Table-driven test for PostMessagesHandler. Same shape as
// postratememory_test.go — add a case by appending a row.
func TestPostMessagesHandler(t *testing.T) {
	testCases := []struct {
		name string

		// Input shape.
		body string // raw JSON

		// Fake-recorder behaviour.
		recorderOut string
		recorderErr error

		// Expected response.
		expectedStatus int
		expectContains string

		// Captured-input assertions (skipped if expectGotSession is empty).
		expectGotSession  string
		expectGotCwd      string
		expectGotMessages int
	}{
		{
			name:           "happy path records and echoes count",
			body:           `{"session_id":"s1","cwd":"/repo","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hey"}]}`,
			recorderOut:    "github.com/owner/repo",
			expectedStatus: http.StatusOK,
			expectContains: `"count":2`,
			expectGotSession:  "s1",
			expectGotCwd:      "/repo",
			expectGotMessages: 2,
		},
		{
			name:           "missing session_id rejected",
			body:           `{"messages":[{"role":"user","content":"hi"}]}`,
			expectedStatus: http.StatusBadRequest,
			expectContains: "session_id is required",
		},
		{
			name:           "empty messages returns 204",
			body:           `{"session_id":"s1","messages":[]}`,
			expectedStatus: http.StatusNoContent,
		},
		{
			name:           "malformed JSON rejected",
			body:           `{not json`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "project_id override is forwarded",
			body:           `{"session_id":"s1","project_id":"explicit-id","messages":[{"role":"user","content":"x"},{"role":"assistant","content":"y"}]}`,
			recorderOut:    "explicit-id",
			expectedStatus: http.StatusOK,
			expectContains: `"project_id":"explicit-id"`,
			expectGotSession:  "s1",
			expectGotMessages: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			recorder := &fakeMessageRecorder{recordOut: tc.recorderOut, recordErr: tc.recorderErr}
			handler := NewPostMessagesHandler(PostMessagesHandlerParams{Recorder: recorder})

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/messages",
				strings.NewReader(tc.body),
			)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code, "status code")
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			if tc.expectGotSession != "" {
				assert.Equal(tc.expectGotSession, recorder.gotSessionID, "recorder saw session_id")
				assert.Len(recorder.gotMessages, tc.expectGotMessages, "recorder saw N messages")
			}
			if tc.expectGotCwd != "" {
				assert.Equal(tc.expectGotCwd, recorder.gotCwd, "recorder saw cwd")
			}
		})
	}
}
