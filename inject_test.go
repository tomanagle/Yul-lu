package main

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInferCategories(t *testing.T) {
	testCases := []struct {
		name   string
		prompt string
		want   []string // sorted; nil = expects nil result
	}{
		{
			name:   "UI-flavoured prompt → style + process",
			prompt: "how do i add a button to the dashboard?",
			want:   []string{"process", "style"},
		},
		{
			name:   "Why prompts → decision",
			prompt: "why did we choose Bun over Node?",
			want:   []string{"decision"},
		},
		{
			name: "Bug / fix prompts → gotcha + decision (+ process when deploy/build words present)",
			// "deploy" also hits process — the heuristic ORs every
			// category match together. Documenting current behaviour.
			prompt: "the deploy keeps failing because of a race condition",
			want:   []string{"decision", "gotcha", "process"},
		},
		{
			name:   "Build / test prompts → process",
			prompt: "how do we run the test suite?",
			want:   []string{"process"},
		},
		{
			name:   "Glossary prompts → domain",
			prompt: "what does dreaming mean here?",
			want:   []string{"domain"},
		},
		{
			name:   "Ambiguous prompt → nil",
			prompt: "hello there friend",
			want:   nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferCategories(tc.prompt)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			sort.Strings(got)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsTrivialPrompt(t *testing.T) {
	testCases := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"empty", "", true},
		{"very short", "hi", true},
		{"thank you", "thank you", true},
		{"ok", "ok", true},
		{"substantive question", "how do I run the tests?", false},
		{"borderline length real question", "fix bug", true}, // <8 chars
		{"single-word real ask but short", "deploy", true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTrivialPrompt(tc.prompt))
		})
	}
}

func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("the quick brown fox", "quick", "missing"))
	assert.False(t, containsAny("hello world", "missing", "absent"))
	assert.False(t, containsAny("", "needle"))
}
