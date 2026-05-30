package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFlattenContent(t *testing.T) {
	testCases := []struct {
		name string
		raw  string // raw JSON value (string or array)
		want string
	}{
		{"plain string", `"hi there"`, "hi there"},
		{"empty string", `""`, ""},
		{"empty array", `[]`, ""},
		{
			name: "text + tool_use mix (tool_use skipped)",
			raw:  `[{"type":"text","text":"a"},{"type":"tool_use","name":"X"},{"type":"text","text":"b"}]`,
			want: "a\nb",
		},
		{
			name: "tool_result skipped",
			raw:  `[{"type":"tool_result","content":"x"},{"type":"text","text":"only this"}]`,
			want: "only this",
		},
		{"malformed JSON returns empty", `not json`, ""},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := flattenContent(json.RawMessage(tc.raw))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsWorthRecording(t *testing.T) {
	testCases := []struct {
		name      string
		user      string
		assistant string
		want      bool
	}{
		{"empty user", "", "long enough assistant reply with substance", false},
		{"empty assistant", "real question", "", false},
		{"single-word + short assistant", "ok", "short", false},
		{"single-word user + long assistant", "ok", "the substantive reply has many words here and there", true},
		{"real exchange", "why?", "long explanatory reply that runs past forty chars", true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isWorthRecording(tc.user, tc.assistant))
		})
	}
}
