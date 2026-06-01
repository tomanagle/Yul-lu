package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runRecordTurn implements `yullu record-turn` — the Claude Code Stop hook
// entry point. Reads the hook's JSON payload from stdin, parses the
// referenced transcript to extract the most recent user/assistant turn,
// and POSTs the pair to the local yullu server at /api/messages.
//
// Failures are deliberately silent — this is best-effort telemetry that
// runs after EVERY turn, so it must never disturb the session. Anything
// that goes sideways (yullu not running → connection refused, transcript
// unparseable, trivial turn) logs to stderr (visible under `claude
// --debug`) and exits 0. We never exit non-zero: Claude Code surfaces a
// non-zero Stop hook as a "non-blocking" error in the user's transcript,
// and exit 2 as a blocking error fed to the model — neither is appropriate
// for telemetry. So if the server's down, the turn just isn't recorded and
// the session carries on as if the hook weren't there.
//
// Hook payload shape (Claude Code docs):
//
//	{
//	  "session_id": "abc123",
//	  "transcript_path": "/path/to/transcript.jsonl",
//	  "cwd": "/current/working/dir",
//	  "permission_mode": "default",
//	  "hook_event_name": "Stop"
//	}
func runRecordTurn() int {
	var hookIn struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		Cwd            string `json:"cwd"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&hookIn); err != nil {
		fmt.Fprintln(os.Stderr, "yullu record-turn: read hook input:", err)
		return 0
	}
	if hookIn.SessionID == "" || hookIn.TranscriptPath == "" {
		fmt.Fprintln(os.Stderr, "yullu record-turn: missing session_id or transcript_path")
		return 0
	}

	userText, assistantText, err := extractLastTurn(hookIn.TranscriptPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "yullu record-turn: extract turn:", err)
		return 0
	}
	if !isWorthRecording(userText, assistantText) {
		// Trivial turn (greeting, single-word ack, tool-only). Skip silently.
		return 0
	}

	// Forward Claude Code's cwd so the server resolves the project from
	// the directory the user is *actually* working in. Without this, the
	// server falls back to os.Getwd() — which is the directory yullu
	// itself was launched from, so every recorded turn ends up scoped to
	// yullu's own repo regardless of where the user is.
	body, _ := json.Marshal(map[string]any{
		"session_id": hookIn.SessionID,
		"cwd":        hookIn.Cwd,
		"messages": []map[string]string{
			{"role": "user", "content": userText},
			{"role": "assistant", "content": assistantText},
		},
	})
	req, err := http.NewRequest(http.MethodPost,
		"http://localhost:47823/api/messages", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "yullu record-turn: build request:", err)
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	// Short timeout — the hook runs in the user's response path. If yullu's
	// down, fail fast rather than blocking the prompt return.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Server unreachable (not running, or slow past the timeout). Expected
		// whenever the desktop app is closed — skip silently, don't nag.
		fmt.Fprintln(os.Stderr, "yullu record-turn: server unreachable, skipping turn:", err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, "yullu record-turn: server returned", resp.Status)
		return 0
	}
	return 0
}

// extractLastTurn walks the transcript backwards and returns the most
// recent user input + the assistant response that followed. The transcript
// is JSONL where each line has a `type` field; `type: "user"` lines whose
// content is purely tool_result blocks are skipped (those are tool
// responses being fed back to the model, not a real user message).
func extractLastTurn(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	// We don't expect transcripts to be huge per-line but raise the scanner
	// buffer anyway — long assistant turns can blow past the default.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	type entry struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}

	var lastUserText, lastAssistantText string
	// Walk forward, overwriting on each new user/assistant entry. The last
	// values seen are the most recent turn. We can't easily walk backward
	// over a scanner — the full transcript fits in memory at this scale.
	for scanner.Scan() {
		var e entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		switch e.Type {
		case "user":
			text := flattenContent(e.Message.Content)
			if text != "" {
				lastUserText = text
				// Reset the assistant trail — we want the response that
				// followed THIS user message, not an earlier one.
				lastAssistantText = ""
			}
		case "assistant":
			text := flattenContent(e.Message.Content)
			if text != "" {
				// Concatenate streaming chunks if there are multiple
				// assistant entries between user messages.
				if lastAssistantText != "" {
					lastAssistantText += "\n"
				}
				lastAssistantText += text
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("scan: %w", err)
	}
	return lastUserText, lastAssistantText, nil
}

// flattenContent extracts plain text from a message's content field, which
// can be either a JSON string (older transcripts) or an array of blocks
// (current format). Returns the concatenation of all `text` blocks; ignores
// tool_use, tool_result, image, and other non-text block types — those
// aren't meaningful inputs for dream-pass extraction.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// String form: "...content..."
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	// Array of blocks: [{"type": "text", "text": "..."}, ...]
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
		}
	}
	return strings.TrimSpace(sb.String())
}

// isWorthRecording filters out trivial exchanges — single-word acks, empty
// turns, etc. The Go side has more conservative rules than the SKILL.md
// because we can't tell the model not to fire the hook; we just drop the
// payload before the round-trip. Errs on the side of recording (when
// uncertain, record).
func isWorthRecording(userText, assistantText string) bool {
	if userText == "" || assistantText == "" {
		return false
	}
	// Very short user messages with no substantive content. We tolerate
	// short queries ("why?", "fix it") because they often follow a richer
	// assistant turn — assistant context is what carries signal here.
	if len(strings.Fields(userText)) <= 1 && len(assistantText) < 40 {
		return false
	}
	return true
}
