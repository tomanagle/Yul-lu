package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/tomanagle/yullu/internal/ai"
	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/store"
)

// DreamResult summarizes a single dream pass.
type DreamResult struct {
	ProjectID         string         `json:"project_id"`
	SessionsProcessed int            `json:"sessions_processed"`
	MessagesProcessed int            `json:"messages_processed"`
	OpsCreated        int            `json:"ops_created"`
	OpsUpdated        int            `json:"ops_updated"`
	OpsDeleted        int            `json:"ops_deleted"`
	OpsSkipped        int            `json:"ops_skipped"`
	Sessions          []DreamSession `json:"sessions,omitempty"`
	Errors            []string       `json:"errors,omitempty"`
	Skipped           bool           `json:"skipped,omitempty"`
}

// DreamSession is one session's result inside a dream pass.
type DreamSession struct {
	SessionID         string `json:"session_id"`
	MessagesProcessed int    `json:"messages_processed"`
	OpsCreated        int    `json:"ops_created"`
	OpsUpdated        int    `json:"ops_updated"`
	OpsDeleted        int    `json:"ops_deleted"`
	OpsSkipped        int    `json:"ops_skipped"`
}

// DreamOptions tunes one dream pass. Zero values mean "no filtering".
type DreamOptions struct {
	// SessionFilter restricts to a single session ID. Empty means all.
	SessionFilter string
	// MinMessages skips sessions with fewer messages than this. The
	// scheduler passes cfg.Dreaming.MinMessages; dream_now passes 0.
	MinMessages int
	// ContextMemories caps how many existing memories the reasoner sees.
	// 0 means use the configured default.
	ContextMemories int
	// ProjectID overrides the project the dream operates on. Empty resolves
	// from the server's CWD (the normal path for the CLI); the desktop app
	// passes an explicit ID so it can dream any project.
	ProjectID string
}

const defaultContextMemories = 50

// Dream processes session messages: feeds each session (or a single named
// one) to the reasoner, applies the operations it returns, and deletes the
// processed messages.
//
// Single-flight: a second concurrent Dream returns immediately with
// Skipped = true. New record_messages calls during a dream are safe - they
// land outside the snapshot the dreamer is operating on and get picked up
// next pass.
func (s *Server) Dream(ctx context.Context, opts DreamOptions) (*DreamResult, error) {
	if !s.dreamMu.TryLock() {
		return &DreamResult{Skipped: true}, nil
	}
	defer s.dreamMu.Unlock()

	projectID := opts.ProjectID
	if projectID == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve cwd: %w", err)
		}
		projectID, err = scope.Resolve(cwd)
		if err != nil {
			return nil, fmt.Errorf("resolve project: %w", err)
		}
	}
	res := &DreamResult{ProjectID: projectID}

	sessionIDs, err := s.store.SessionsWithMessages(ctx, projectID, opts.MinMessages)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if opts.SessionFilter != "" {
		// Only keep the requested session if it's in the list (otherwise
		// there's nothing to dream).
		filtered := make([]string, 0, 1)
		for _, sid := range sessionIDs {
			if sid == opts.SessionFilter {
				filtered = append(filtered, sid)
				break
			}
		}
		sessionIDs = filtered
	}

	contextLimit := opts.ContextMemories
	if contextLimit <= 0 {
		contextLimit = defaultContextMemories
	}
	memories, err := s.store.List(ctx, projectID, contextLimit)
	if err != nil {
		return nil, fmt.Errorf("load context memories: %w", err)
	}

	for _, sid := range sessionIDs {
		sessRes, err := s.dreamSession(ctx, projectID, sid, memories)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("session %s: %v", sid, err))
			continue
		}
		res.Sessions = append(res.Sessions, sessRes)
		res.SessionsProcessed++
		res.MessagesProcessed += sessRes.MessagesProcessed
		res.OpsCreated += sessRes.OpsCreated
		res.OpsUpdated += sessRes.OpsUpdated
		res.OpsDeleted += sessRes.OpsDeleted
		res.OpsSkipped += sessRes.OpsSkipped
	}
	return res, nil
}

// dreamSession dreams one session: builds the prompt, calls the reasoner,
// parses ops, applies them, then deletes the processed message rows.
func (s *Server) dreamSession(ctx context.Context, projectID, sessionID string, memories []store.Memory) (DreamSession, error) {
	res := DreamSession{SessionID: sessionID}

	msgs, err := s.store.SessionMessages(ctx, sessionID)
	if err != nil {
		return res, fmt.Errorf("load messages: %w", err)
	}
	if len(msgs) == 0 {
		return res, nil
	}
	res.MessagesProcessed = len(msgs)

	prompt := buildDreamUserPrompt(memories, msgs)
	raw, err := s.callReasoner(ctx, dreamSystemPrompt, prompt, 4000)
	if err != nil {
		return res, fmt.Errorf("reasoner: %w", err)
	}
	ops, parseErr := parseDreamResponse(raw)
	if parseErr != nil {
		// Keep the messages so a later dream can try again with a (hopefully)
		// better response. Surface the error to the caller via DreamResult.
		s.logger.Warn("dream response parse failed; keeping messages",
			"session_id", sessionID,
			"err", parseErr.Error(),
			"raw_excerpt", excerpt(raw, 300),
		)
		return res, parseErr
	}

	// Capture message IDs before applying ops so we can delete them after
	// the apply pass - keeping the messages on failure would let us retry
	// next time, but we accept partial failures and delete unconditionally.
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}

	for _, op := range ops {
		s.applyDreamOp(ctx, projectID, op, &res)
	}

	if err := s.store.DeleteSessionMessages(ctx, ids); err != nil {
		s.logger.Warn("failed to delete session messages after dreaming",
			"session_id", sessionID, "err", err.Error())
		return res, fmt.Errorf("cleanup: %w", err)
	}

	s.logger.Info("dream session completed",
		"session_id", sessionID,
		"messages", res.MessagesProcessed,
		"created", res.OpsCreated,
		"updated", res.OpsUpdated,
		"deleted", res.OpsDeleted,
		"skipped", res.OpsSkipped,
	)
	return res, nil
}

// applyDreamOp routes one reasoner-emitted operation through the shared
// memory write path. Skipped ops are counted but don't fail the dream.
func (s *Server) applyDreamOp(ctx context.Context, projectID string, op dreamOp, res *DreamSession) {
	switch op.Op {
	case "create":
		if op.Content == "" {
			s.logger.Warn("dream op skipped: create without content", "reasoning", op.Reasoning)
			res.OpsSkipped++
			return
		}
		tags := []string{}
		if op.Tags != nil {
			tags = *op.Tags
		}
		uuid, _, err := s.createMemory(ctx, projectID, op.Content, tags)
		if err != nil {
			s.logger.Warn("dream create failed", "err", err.Error())
			res.OpsSkipped++
			return
		}
		s.logger.Info("dream created memory", "uuid", uuid, "reasoning", op.Reasoning)
		res.OpsCreated++
	case "update":
		if op.UUID == "" {
			res.OpsSkipped++
			return
		}
		var contentPtr *string
		if op.Content != "" {
			c := op.Content
			contentPtr = &c
		}
		tagsPtr := op.Tags
		if contentPtr == nil && tagsPtr == nil {
			res.OpsSkipped++
			return
		}
		if _, err := s.updateMemoryByUUID(ctx, op.UUID, contentPtr, tagsPtr); err != nil {
			s.logger.Warn("dream update failed", "uuid", op.UUID, "err", err.Error())
			res.OpsSkipped++
			return
		}
		s.logger.Info("dream updated memory", "uuid", op.UUID, "reasoning", op.Reasoning)
		res.OpsUpdated++
	case "delete":
		if op.UUID == "" {
			res.OpsSkipped++
			return
		}
		if err := s.deleteMemoryByUUID(ctx, op.UUID); err != nil {
			s.logger.Warn("dream delete failed", "uuid", op.UUID, "err", err.Error())
			res.OpsSkipped++
			return
		}
		s.logger.Info("dream deleted memory", "uuid", op.UUID, "reasoning", op.Reasoning)
		res.OpsDeleted++
	default:
		s.logger.Warn("dream op skipped: unknown op type", "op", op.Op)
		res.OpsSkipped++
	}
}

// StartScheduler spawns the background dream loop. Returns immediately if
// dreaming is disabled. The goroutine exits when ctx is cancelled.
//
// Strategy: a single ticker polls on a short cadence (capped at 5s) and
// fires Dream when either (a) interval has elapsed since the last dream
// or (b) record_messages has been quiet for on_idle_seconds and there are
// unprocessed messages. The single-flight lock inside Dream means the
// scheduler can't collide with dream_now.
func (s *Server) StartScheduler(ctx context.Context) {
	if !s.dreamCfg.Enabled {
		return
	}
	interval := s.dreamCfg.IntervalDuration()
	idle := time.Duration(s.dreamCfg.OnIdleSeconds) * time.Second

	poll := 5 * time.Second
	if interval < poll {
		poll = interval
	}
	if idle > 0 && idle < poll {
		poll = idle
	}
	if poll < time.Second {
		poll = time.Second
	}
	s.logger.Info("dream scheduler started",
		"interval", interval.String(),
		"min_messages", s.dreamCfg.MinMessages,
		"on_idle_seconds", s.dreamCfg.OnIdleSeconds,
		"poll", poll.String(),
	)
	go s.runScheduler(ctx, interval, idle, poll)
}

func (s *Server) runScheduler(ctx context.Context, interval, idle, poll time.Duration) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	lastDream := time.Time{}

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("dream scheduler stopping")
			return
		case now := <-ticker.C:
			if s.shouldDream(ctx, now, lastDream, interval, idle) {
				s.runScheduledDream(ctx)
				lastDream = now
			}
		}
	}
}

// shouldDream returns true if either the interval or the idle trigger has
// fired. lastDream may be the zero value (first iteration); in that case
// the interval branch fires immediately so the first scheduled dream
// happens promptly after server boot.
func (s *Server) shouldDream(ctx context.Context, now, lastDream time.Time, interval, idle time.Duration) bool {
	if now.Sub(lastDream) >= interval {
		return true
	}
	if idle <= 0 {
		return false
	}
	lastMsg := s.lastMessageTime()
	if lastMsg.IsZero() || now.Sub(lastMsg) < idle {
		return false
	}
	// Cheap pre-check - don't even fire if there's nothing to do.
	return s.hasUnprocessedMessages(ctx)
}

func (s *Server) hasUnprocessedMessages(ctx context.Context) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	projectID, err := scope.Resolve(cwd)
	if err != nil {
		return false
	}
	sessions, err := s.store.SessionsWithMessages(ctx, projectID, 1)
	if err != nil {
		return false
	}
	return len(sessions) > 0
}

func (s *Server) runScheduledDream(ctx context.Context) {
	res, err := s.Dream(ctx, DreamOptions{
		MinMessages:     s.dreamCfg.MinMessages,
		ContextMemories: s.dreamCfg.ContextMemories,
	})
	if err != nil {
		s.logger.Error("scheduled dream failed", "err", err.Error())
		return
	}
	if res.Skipped || res.SessionsProcessed == 0 {
		return
	}
	s.logger.Info("scheduled dream completed",
		"sessions", res.SessionsProcessed,
		"messages", res.MessagesProcessed,
		"created", res.OpsCreated,
		"updated", res.OpsUpdated,
		"deleted", res.OpsDeleted,
		"skipped_ops", res.OpsSkipped,
	)
}

// dreamOp is one operation in the reasoner's structured JSON response.
type dreamOp struct {
	Op        string    `json:"op"`
	UUID      string    `json:"uuid,omitempty"`
	Content   string    `json:"content,omitempty"`
	Tags      *[]string `json:"tags,omitempty"`
	Reasoning string    `json:"reasoning"`
}

type dreamResponse struct {
	Operations []dreamOp `json:"operations"`
}

// parseDreamResponse extracts the JSON body from raw (tolerating optional
// ```json``` fences and surrounding whitespace) and decodes into
// dreamResponse. Returns the operations list or an error.
func parseDreamResponse(raw string) ([]dreamOp, error) {
	body := strings.TrimSpace(raw)
	body = stripCodeFence(body)
	body = extractFirstJSONObject(body)
	if body == "" {
		return nil, fmt.Errorf("no JSON object found in reasoner response")
	}
	var parsed dreamResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return parsed.Operations, nil
}

// stripCodeFence removes ```json … ``` or ``` … ``` wrappers if present.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence (and any language tag after it).
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	} else {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// extractFirstJSONObject returns the substring from the first `{` to its
// matching `}`. Tolerant of leading/trailing prose around well-formed JSON.
func extractFirstJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inStr {
			if c == '\\' {
				escape = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

const dreamSystemPrompt = `You are a memory curator for a software engineer's coding session.

Review the conversation and the current memories for this codebase, then decide which durable facts are worth keeping for FUTURE sessions.

WORTH REMEMBERING:
- Decisions and their WHY (why we chose X over Y).
- Gotchas and non-obvious constraints.
- Project facts not derivable from reading the code (incidents, conventions, external dependencies, team agreements).
- Updates that correct or extend an existing memory.

SKIP:
- Trivia, generic programming advice, anything in standard documentation.
- Ephemeral state ("I'm working on X right now").
- Things obvious from reading the code or git history.
- Task-specific noise.

OUTPUT FORMAT - strict JSON only, no prose, no markdown fences:
{
  "operations": [
    {"op": "create", "content": "...", "tags": ["..."], "reasoning": "..."},
    {"op": "update", "uuid": "<existing-uuid>", "content": "...", "tags": ["..."], "reasoning": "..."},
    {"op": "delete", "uuid": "<existing-uuid>", "reasoning": "..."}
  ]
}

For each operation, "reasoning" is a one-sentence justification. New memories should be specific and self-contained - include the why, not just the what. If nothing is worth changing, return {"operations": []}.`

// callReasoner asks the LLM for a completion. Prefers MCP sampling - the
// client (Claude Code, Codex, etc.) handles the call using its own
// credentials, so users with Pro/Plus subscriptions don't need a separate
// API key. Falls back to the configured direct Reasoner when sampling
// isn't available (no active session, client without sampling capability,
// or background scheduled dreams).
func (s *Server) callReasoner(ctx context.Context, system, userPrompt string, maxTokens int) (string, error) {
	// Capture why sampling didn't happen so the final error message is
	// diagnostic. Three failure modes worth distinguishing:
	//   (a) no MCP client session in ctx  - background work, desktop button
	//   (b) session exists but client refused sampling - Claude Code without
	//       sampling support, or user denied the consent prompt
	//   (c) sampling returned a malformed response
	var samplingReason string
	session := mcpsrv.ClientSessionFromContext(ctx)
	if session == nil {
		samplingReason = "no active MCP client session (background scheduler or desktop button)"
	} else {
		req := mcp.CreateMessageRequest{
			CreateMessageParams: mcp.CreateMessageParams{
				SystemPrompt: system,
				MaxTokens:    maxTokens,
				Messages: []mcp.SamplingMessage{
					{
						Role:    mcp.RoleUser,
						Content: mcp.TextContent{Type: "text", Text: userPrompt},
					},
				},
			},
		}
		result, err := s.mcp.RequestSampling(ctx, req)
		if err == nil {
			if txt, ok := result.Content.(mcp.TextContent); ok {
				return txt.Text, nil
			}
			// Some clients return content as map[string]any after JSON decode.
			if m, ok := result.Content.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					return t, nil
				}
			}
			samplingReason = fmt.Sprintf("response had unexpected content type %T", result.Content)
		} else {
			samplingReason = "client rejected sampling: " + err.Error()
		}
		s.logger.Debug("sampling unavailable; falling back to direct reasoner",
			"reason", samplingReason,
			"session_type", fmt.Sprintf("%T", session),
		)
	}

	if s.reasoner == nil {
		return "", fmt.Errorf("no reasoner available - sampling didn't work (%s) and no direct provider is configured. Set [reasoning].provider in Settings to anthropic or openai with an API key, OR ensure your MCP client supports sampling", samplingReason)
	}
	return s.reasoner.Reason(ctx, ai.ReasonRequest{
		System: system,
		Messages: []ai.Message{
			{Role: "user", Content: userPrompt},
		},
		MaxTokens: maxTokens,
	})
}

func buildDreamUserPrompt(memories []store.Memory, msgs []store.SessionMessage) string {
	var buf strings.Builder
	buf.WriteString("CURRENT MEMORIES:\n")
	if len(memories) == 0 {
		buf.WriteString("(none yet)\n")
	} else {
		for _, m := range memories {
			fmt.Fprintf(&buf, "- uuid=%s tags=%v\n  %s\n", m.UUID, m.Tags, m.Content)
		}
	}
	buf.WriteString("\nCONVERSATION:\n")
	for _, m := range msgs {
		fmt.Fprintf(&buf, "[%s] %s\n", m.Role, m.Content)
	}
	return buf.String()
}
