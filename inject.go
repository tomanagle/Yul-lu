package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runInject implements `yullu inject` — the Claude Code UserPromptSubmit
// hook entry point. Reads the hook's JSON payload from stdin, asks the
// running Yul'lu server which memories are relevant to the user's prompt,
// and prints them to stdout as additional context. Claude Code prepends
// stdout to the conversation before the model processes the user's
// message.
//
// Failures are deliberately non-fatal (exit 1, never 2). UserPromptSubmit
// treats exit-2 as blocking; we never want to prevent the user's prompt
// from being processed — at worst, no memories get injected this turn.
//
// Hook payload shape (Claude Code docs):
//
//	{
//	  "session_id":      "abc123",
//	  "transcript_path": "/path/to/transcript.jsonl",
//	  "cwd":             "/current/working/dir",
//	  "prompt":          "the user's message verbatim",
//	  "hook_event_name": "UserPromptSubmit"
//	}
func runInject() int {
	var hookIn struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Prompt    string `json:"prompt"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&hookIn); err != nil {
		fmt.Fprintln(os.Stderr, "yullu inject: read hook input:", err)
		return 1
	}
	prompt := strings.TrimSpace(hookIn.Prompt)
	if prompt == "" {
		return 0 // nothing to recall against
	}
	// Trivial-prompt filter — single-word inputs, pleasantries, etc.
	// rarely yield useful retrievals and would spend tokens on noise.
	if isTrivialPrompt(prompt) {
		return 0
	}

	body, _ := json.Marshal(map[string]any{
		"query":      prompt,
		"cwd":        hookIn.Cwd,
		"categories": inferCategories(prompt),
		"limit":      5,
	})
	req, err := http.NewRequest(http.MethodPost,
		"http://localhost:47823/api/memories/recall", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "yullu inject: build request:", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	// Short timeout — we sit in the user's response path. Better to
	// inject nothing than to delay the conversation.
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		// Server probably not running. Silently no-op.
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return 0
	}

	var view struct {
		ProjectID string `json:"project_id"`
		Results   []struct {
			Content  string   `json:"content"`
			Category string   `json:"category"`
			Tags     []string `json:"tags"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		return 0
	}
	if len(view.Results) == 0 {
		return 0
	}

	// Render as a markdown block prepended to the conversation. The
	// agent reads stdout as additional system context. Mark it clearly
	// so the model knows the source.
	fmt.Println("<yullu-memories>")
	fmt.Printf("The following project memories may be relevant to the user's request (project: %s).\n", view.ProjectID)
	fmt.Println("These are durable facts about this codebase — treat them as ground truth unless the user contradicts them.")
	fmt.Println()
	for _, r := range view.Results {
		if r.Category != "" {
			fmt.Printf("- [%s] %s\n", r.Category, r.Content)
		} else {
			fmt.Printf("- %s\n", r.Content)
		}
	}
	fmt.Println("</yullu-memories>")
	return 0
}

// inferCategories maps a user prompt to a likely category set. Heuristic,
// not perfect — when the heuristic is wrong, the server still returns
// vector-similar memories from any category, just biased toward the
// filter. The bias is the whole point: a UI-shaped prompt prefers UI
// memories even when a process memory is semantically close.
//
// Returns nil when the prompt is shape-ambiguous — server then returns
// from all categories. That's the right default: better to return
// something useful than to over-filter and return nothing.
func inferCategories(prompt string) []string {
	p := strings.ToLower(prompt)
	cats := make(map[string]bool, 3)

	// UI / style signals
	if containsAny(p, "ui", "component", "button", "card", "modal", "color", "colour",
		"layout", "padding", "margin", "design", "style", "css", "tailwind",
		"icon", "page", "screen", "form", "input", "tab", "sidebar", "menu",
		"copy", "wording", "label") {
		cats["style"] = true
		cats["process"] = true
	}

	// Decision / why signals
	if containsAny(p, "why", "rationale", "decided", "choose", "chose", "tradeoff", "trade-off",
		"prefer", "compared to", "vs ", "instead of") {
		cats["decision"] = true
	}

	// Gotcha / debug / change-existing signals
	if containsAny(p, "bug", "broken", "error", "fix", "crash", "race", "deadlock",
		"slow", "timeout", "fails", "doesn't work", "doesnt work", "regression") {
		cats["gotcha"] = true
		cats["decision"] = true
	}

	// Process / how signals
	if containsAny(p, "how do i", "how do we", "build", "test", "deploy", "run ",
		"command", "script", "where do", "where does", "add a ", "create a ",
		"new ", "scaffold", "convention", "naming") {
		cats["process"] = true
	}

	// Domain / glossary signals
	if containsAny(p, "what does", "what is", "what's a", "means", "meaning",
		"define", "definition") {
		cats["domain"] = true
	}

	if len(cats) == 0 {
		return nil // ambiguous — let the server return cross-category
	}
	out := make([]string, 0, len(cats))
	for k := range cats {
		out = append(out, k)
	}
	return out
}

// containsAny reports whether p contains any of the needles. Substring
// match — needles should include surrounding spaces if word-boundaries
// matter (e.g. "vs " not "vs", to avoid matching "lvs" in URLs).
func containsAny(p string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(p, n) {
			return true
		}
	}
	return false
}

// isTrivialPrompt filters out one-word acks, pleasantries, and very
// short inputs that are unlikely to benefit from memory injection.
// Conservative: when in doubt, return false and let the server decide
// if any memories match.
func isTrivialPrompt(p string) bool {
	if len(p) < 8 {
		return true
	}
	lower := strings.ToLower(p)
	switch lower {
	case "thanks", "thank you", "ok", "okay", "cool", "got it", "fine",
		"yes", "no", "yep", "nope", "sure", "go ahead", "do it", "continue":
		return true
	}
	return false
}
