package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPostRetryHandler(t *testing.T) {
	testCases := []struct {
		name string

		retryOut Status

		expectedStatus int
		expectReady    bool
	}{
		{
			name:           "retry returns ready=true",
			retryOut:       Status{Ready: true},
			expectedStatus: http.StatusOK,
			expectReady:    true,
		},
		{
			name:           "retry still failing returns ready=false with hint",
			retryOut:       Status{Ready: false, Message: "still broken"},
			expectedStatus: http.StatusOK,
			expectReady:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			handler := NewPostRetryHandler(PostRetryHandlerParams{
				Status: &fakeStatusService{retryOut: tc.retryOut},
			})

			req := httptest.NewRequest(http.MethodPost, "/api/retry", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(tc.expectedStatus, rr.Code)
			var got Status
			assert.NoError(json.NewDecoder(rr.Body).Decode(&got))
			assert.Equal(tc.expectReady, got.Ready)
		})
	}
}
