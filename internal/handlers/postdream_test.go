package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomanagle/yullu/internal/server"
)

func TestPostDreamHandler(t *testing.T) {
	testCases := []struct {
		name string

		body string

		dreamerOut *server.DreamResult
		dreamerErr error

		// Thunk return value for ContextMemories.
		ctxMemories int

		expectedStatus  int
		expectContains  string
		expectGotProject string
		expectGotCtxMem  int
	}{
		{
			name:           "happy path with project_id forwards opts",
			body:           `{"project_id":"p1"}`,
			dreamerOut:     &server.DreamResult{ProjectID: "p1", SessionsProcessed: 2},
			ctxMemories:    50,
			expectedStatus: http.StatusOK,
			expectContains: `"sessions_processed":2`,
			expectGotProject: "p1",
			expectGotCtxMem:  50,
		},
		{
			name:           "empty body still calls dreamer",
			body:           ``,
			dreamerOut:     &server.DreamResult{},
			ctxMemories:    20,
			expectedStatus: http.StatusOK,
			expectGotCtxMem: 20,
		},
		{
			name:           "dreamer error is 500",
			body:           `{"project_id":"p1"}`,
			dreamerErr:     assertErr("reasoner down"),
			ctxMemories:    50,
			expectedStatus: http.StatusInternalServerError,
			expectContains: "reasoner down",
			// The thunk still resolves before Dream is called, so opts
			// gets stamped before the error propagates.
			expectGotCtxMem: 50,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			dreamer := &fakeDreamer{dreamOut: tc.dreamerOut, dreamErr: tc.dreamerErr}
			ctxMem := tc.ctxMemories
			handler := NewPostDreamHandler(PostDreamHandlerParams{
				Dreamer:         dreamer,
				ContextMemories: func() int { return ctxMem },
			})

			req := httptest.NewRequest(http.MethodPost, "/api/dream", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			if tc.expectContains != "" {
				assert.Contains(rr.Body.String(), tc.expectContains)
			}
			if tc.expectGotProject != "" {
				assert.Equal(tc.expectGotProject, dreamer.gotOpts.ProjectID)
			}
			assert.Equal(tc.expectGotCtxMem, dreamer.gotOpts.ContextMemories, "thunk-resolved ctx memories")
		})
	}
}
