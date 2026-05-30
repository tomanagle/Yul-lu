package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Table-driven test for GetStatusHandler. Add a new case by appending a
// row to testCases — each row drives a fakeStatusService and asserts the
// response shape. See getabout_test.go in github.com/TomDoesTech/GOTTH
// for the canonical reference.
func TestGetStatusHandler(t *testing.T) {
	testCases := []struct {
		name           string
		status         Status
		expectedStatus int
		expectReady    bool
		expectMessage  string
	}{
		{
			name: "ready store reports ready",
			status: Status{
				Ready:      true,
				ConfigPath: "/cfg.toml",
				DBPath:     "/mem.db",
				Embedder:   "voyage",
			},
			expectedStatus: http.StatusOK,
			expectReady:    true,
		},
		{
			name: "no API key surfaces hint",
			status: Status{
				Ready:   false,
				Message: "No API key configured.",
				Hint:    "Add a Voyage or OpenAI key in Settings.",
			},
			expectedStatus: http.StatusOK,
			expectReady:    false,
			expectMessage:  "No API key configured.",
		},
		{
			name:           "empty status still serialises",
			status:         Status{},
			expectedStatus: http.StatusOK,
			expectReady:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			handler := NewGetStatusHandler(GetStatusHandlerParams{
				Status: &fakeStatusService{statusOut: tc.status},
			})

			req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code, "status code")

			var got Status
			assert.NoError(json.NewDecoder(rr.Body).Decode(&got), "response is valid JSON Status")
			assert.Equal(tc.expectReady, got.Ready, "ready flag")
			if tc.expectMessage != "" {
				assert.Equal(tc.expectMessage, got.Message, "message")
			}
		})
	}
}
