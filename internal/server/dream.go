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
	"github.com/tomanagle/yullu/internal/config"
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

// DreamProgress is a live snapshot of the in-flight dream pass (or the
// last one to finish, when Running=false). The UI polls it to render a
// "Dreaming…" indicator with phase + counters, AND to show "next pass
// in N minutes" / "idle trigger fires when buffer goes quiet for N
// seconds" derived from the scheduler config.
type DreamProgress struct {
	Running           bool      `json:"running"`
	ProjectID         string    `json:"project_id,omitempty"`
	Phase             string    `json:"phase,omitempty"`              // "starting" | "session" | "idle"
	StartedAt         time.Time `json:"started_at,omitzero"`
	FinishedAt        time.Time `json:"finished_at,omitzero"`
	TotalSessions     int       `json:"total_sessions"`               // sessions enqueued for this pass
	CompletedSessions int       `json:"completed_sessions"`           // sessions finished so far
	CurrentSessionID  string    `json:"current_session_id,omitempty"` // the session being reasoned about right now
	MessagesProcessed int       `json:"messages_processed"`
	OpsCreated        int       `json:"ops_created"`
	OpsUpdated        int       `json:"ops_updated"`
	OpsDeleted        int       `json:"ops_deleted"`
	OpsSkipped        int       `json:"ops_skipped"`
	LastError         string    `json:"last_error,omitempty"`

	// Scheduler-derived fields. Computed on every snapshot read.
	// SchedulerEnabled is false when [dreaming].enabled is false in the
	// effective config — in that case nothing fires automatically and
	// NextAt is zero.
	SchedulerEnabled  bool          `json:"scheduler_enabled"`
	IntervalSeconds   int           `json:"interval_seconds"`
	OnIdleSeconds     int           `json:"on_idle_seconds"`
	LastMessageAt     time.Time     `json:"last_message_at,omitzero"`
	LastScheduledAt   time.Time     `json:"last_scheduled_at,omitzero"`
	NextIntervalAt    time.Time     `json:"next_interval_at,omitzero"`
	NextIdleAt        time.Time     `json:"next_idle_at,omitzero"`
	// NextAt is the soonest of the two triggers, or zero if neither will
	// fire (scheduler disabled, no buffered messages, etc.). NextReason
	// names which trigger wins: "interval" or "idle".
	NextAt     time.Time `json:"next_at,omitzero"`
	NextReason string    `json:"next_reason,omitempty"`
}

// DreamProgressSnapshot returns the latest DreamProgress safely (a copy
// under the lock — never a pointer to the live struct). Scheduler-derived
// fields are computed fresh on every read so the UI countdown stays
// accurate without us having to wake the scheduler.
func (s *Server) DreamProgressSnapshot() DreamProgress {
	s.dreamProgressMu.Lock()
	snap := s.dreamProgress
	s.dreamProgressMu.Unlock()

	// Resolve the effective config for whichever project the progress
	// snapshot is for (or the global default when ProjectID is empty,
	// e.g. before the first pass).
	cfg := s.resolveProject(snap.ProjectID).Dreaming
	snap.SchedulerEnabled = cfg.Enabled
	interval := cfg.IntervalDuration()
	snap.IntervalSeconds = int(interval.Seconds())
	snap.OnIdleSeconds = cfg.OnIdleSeconds

	s.dreamStateMu.Lock()
	lastMsg := s.lastMessageRecordedAt
	s.dreamStateMu.Unlock()
	snap.LastMessageAt = lastMsg

	s.lastScheduledDreamAtMu.Lock()
	lastSched := s.lastScheduledDreamAt[snap.ProjectID]
	s.lastScheduledDreamAtMu.Unlock()
	snap.LastScheduledAt = lastSched

	if !cfg.Enabled {
		return snap
	}

	now := time.Now()
	// Time trigger: next interval after lastSched (or now if lastSched is
	// zero — first iteration fires promptly).
	if interval > 0 {
		if lastSched.IsZero() {
			snap.NextIntervalAt = now
		} else {
			snap.NextIntervalAt = lastSched.Add(interval)
		}
	}
	// Idle trigger: lastMsg + onIdle, but only meaningful when there's
	// actually been a message recorded since boot. Don't bother checking
	// buffer presence here — the UI shouldn't promise idle-firing if the
	// scheduler will skip the pre-check, but we'd need to hit the DB to
	// know, and this snapshot is meant to be cheap. The UI can just say
	// "idle trigger at X if messages still pending".
	if cfg.OnIdleSeconds > 0 && !lastMsg.IsZero() {
		snap.NextIdleAt = lastMsg.Add(time.Duration(cfg.OnIdleSeconds) * time.Second)
	}

	// Pick the soonest non-zero trigger as the headline NextAt.
	switch {
	case !snap.NextIntervalAt.IsZero() && !snap.NextIdleAt.IsZero():
		if snap.NextIntervalAt.Before(snap.NextIdleAt) {
			snap.NextAt = snap.NextIntervalAt
			snap.NextReason = "interval"
		} else {
			snap.NextAt = snap.NextIdleAt
			snap.NextReason = "idle"
		}
	case !snap.NextIntervalAt.IsZero():
		snap.NextAt = snap.NextIntervalAt
		snap.NextReason = "interval"
	case !snap.NextIdleAt.IsZero():
		snap.NextAt = snap.NextIdleAt
		snap.NextReason = "idle"
	}
	return snap
}

func (s *Server) progressUpdate(mut func(*DreamProgress)) {
	s.dreamProgressMu.Lock()
	defer s.dreamProgressMu.Unlock()
	mut(&s.dreamProgress)
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

	startedAt := time.Now()
	s.progressUpdate(func(p *DreamProgress) {
		*p = DreamProgress{
			Running:   true,
			ProjectID: projectID,
			Phase:     "starting",
			StartedAt: startedAt,
		}
	})
	// Mark idle on every return path — Phase="idle", Running=false. The
	// counters and last error stay populated so the UI can show "last pass
	// finished N seconds ago: created X, updated Y" without a second call.
	defer func() {
		s.progressUpdate(func(p *DreamProgress) {
			p.Running = false
			p.Phase = "idle"
			p.CurrentSessionID = ""
			p.FinishedAt = time.Now()
		})
	}()

	sessionIDs, err := s.store.SessionsWithMessages(ctx, projectID, opts.MinMessages)
	if err != nil {
		s.progressUpdate(func(p *DreamProgress) { p.LastError = err.Error() })
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

	// Anti-examples for the prompt. Up to 5 most recent user-rejected
	// memories per project; if the load errors we just dream without
	// them (best-effort feedback signal, never blocking).
	rejected, _ := s.store.RecentRejected(ctx, projectID, 5)

	s.progressUpdate(func(p *DreamProgress) {
		p.TotalSessions = len(sessionIDs)
	})
	for _, sid := range sessionIDs {
		s.progressUpdate(func(p *DreamProgress) {
			p.Phase = "session"
			p.CurrentSessionID = sid
		})
		sessRes, err := s.dreamSession(ctx, projectID, sid, memories, rejected)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("session %s: %v", sid, err))
			s.progressUpdate(func(p *DreamProgress) {
				p.LastError = fmt.Sprintf("session %s: %v", sid, err)
				p.CompletedSessions++
			})
			continue
		}
		res.Sessions = append(res.Sessions, sessRes)
		res.SessionsProcessed++
		res.MessagesProcessed += sessRes.MessagesProcessed
		res.OpsCreated += sessRes.OpsCreated
		res.OpsUpdated += sessRes.OpsUpdated
		res.OpsDeleted += sessRes.OpsDeleted
		res.OpsSkipped += sessRes.OpsSkipped
		s.progressUpdate(func(p *DreamProgress) {
			p.CompletedSessions++
			p.MessagesProcessed += sessRes.MessagesProcessed
			p.OpsCreated += sessRes.OpsCreated
			p.OpsUpdated += sessRes.OpsUpdated
			p.OpsDeleted += sessRes.OpsDeleted
			p.OpsSkipped += sessRes.OpsSkipped
		})
	}

	// Persist the pass for the Stats dashboard. Only non-skipped passes go
	// to dream_passes (single-flight collisions return Skipped=true above
	// and would skew the averages). Telemetry; never blocks the result.
	s.store.RecordDreamPass(ctx, store.DreamPassRecord{
		ProjectID:         projectID,
		SessionsProcessed: res.SessionsProcessed,
		MessagesProcessed: res.MessagesProcessed,
		OpsCreated:        res.OpsCreated,
		OpsUpdated:        res.OpsUpdated,
		OpsDeleted:        res.OpsDeleted,
		OpsSkipped:        res.OpsSkipped,
		Errors:            res.Errors,
	})
	return res, nil
}

// (continues) dreamSession dreams one session: builds the prompt, calls the reasoner,
// parses ops, applies them, then deletes the processed message rows.
func (s *Server) dreamSession(ctx context.Context, projectID, sessionID string, memories []store.Memory, rejected []store.RejectedMemory) (DreamSession, error) {
	res := DreamSession{SessionID: sessionID}

	msgs, err := s.store.SessionMessages(ctx, projectID, sessionID)
	if err != nil {
		return res, fmt.Errorf("load messages: %w", err)
	}
	if len(msgs) == 0 {
		return res, nil
	}
	res.MessagesProcessed = len(msgs)

	prompt := buildDreamUserPrompt(projectID, memories, msgs, rejected)
	// DreamPromptForReasoner concatenates the user-editable body with the
	// locked OUTPUT FORMAT contract — parseDreamResponse depends on that
	// JSON shape, so it's never part of the editable prompt.
	systemPrompt := config.DreamPromptForReasoner()
	raw, err := s.callReasoner(ctx, systemPrompt, prompt, 4000)
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
		uuid, _, err := s.createMemory(ctx, projectID, op.Content, tags, store.MemoryCategory(op.Category))
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

// StartScheduler spawns the background dream loop. The scheduler always
// starts — per-project gating happens inside scheduleTick, which reads
// each project's effective config. Previously this short-circuited when
// the GLOBAL [dreaming].enabled was off, which meant a per-project
// override of dreaming.enabled = true was silently ignored.
//
// Strategy: a single ticker polls on a short cadence (capped at 5s) and
// for each project with buffered messages, fires Dream when either
// (a) interval has elapsed since the last dream for THAT project or
// (b) record_messages has been quiet for on_idle_seconds. The
// single-flight lock inside Dream means the scheduler can't collide
// with dream_now.
//
// Poll cadence comes from the global config — it bounds responsiveness
// for new pending projects, not per-project firing. Per-project
// interval/idle are read fresh inside scheduleTick.
// StartScheduler spawns the dream loop. Safe to call multiple times —
// each call cancels the prior goroutine before starting a new one, so a
// SaveConfig-triggered rebuild doesn't accumulate phantom schedulers.
func (s *Server) StartScheduler(ctx context.Context) {
	// Cancel any existing scheduler before starting a fresh one.
	s.schedulerMu.Lock()
	if s.schedulerCancel != nil {
		s.schedulerCancel()
	}
	derivedCtx, cancel := context.WithCancel(ctx)
	s.schedulerCancel = cancel
	s.schedulerMu.Unlock()

	interval := s.cfg.Dreaming.IntervalDuration()
	idle := time.Duration(s.cfg.Dreaming.OnIdleSeconds) * time.Second

	poll := 5 * time.Second
	if interval > 0 && interval < poll {
		poll = interval
	}
	if idle > 0 && idle < poll {
		poll = idle
	}
	if poll < time.Second {
		poll = time.Second
	}
	s.logger.Info("dream scheduler started",
		"global_interval", interval.String(),
		"global_min_messages", s.cfg.Dreaming.MinMessages,
		"global_on_idle_seconds", s.cfg.Dreaming.OnIdleSeconds,
		"global_enabled", s.cfg.Dreaming.Enabled,
		"poll", poll.String(),
	)
	// Use the derived ctx so StopScheduler / a fresh StartScheduler
	// (e.g. after SaveConfig builds a new Server and the old one
	// re-cancels) can shut this goroutine down without waiting for
	// the parent ctx to cancel.
	go s.runScheduler(derivedCtx, poll)
}

// StopScheduler cancels the running scheduler goroutine. No-op if
// StartScheduler was never called or has already been stopped.
func (s *Server) StopScheduler() {
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.schedulerCancel != nil {
		s.schedulerCancel()
		s.schedulerCancel = nil
	}
}

// runScheduler polls every project with buffered messages and delegates
// to scheduleTick. The interval / idle thresholds used to live in the
// scheduler's local state but are now read per-project from each
// project's effective config inside scheduleTick — the scheduler itself
// only needs the poll cadence.
func (s *Server) runScheduler(ctx context.Context, poll time.Duration) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("dream scheduler stopping")
			return
		case now := <-ticker.C:
			s.scheduleTick(ctx, now)
		}
	}
}

// scheduleTick enumerates pending projects and dreams the ones whose
// per-project schedule says it's time. Pulled out of runScheduler so it
// can be tested directly with a synthetic `now` without driving a ticker.
func (s *Server) scheduleTick(ctx context.Context, now time.Time) {
	projects, err := s.store.PendingMessageProjects(ctx)
	if err != nil {
		s.logger.Warn("scheduler: list pending projects", "err", err.Error())
		return
	}
	if len(projects) == 0 {
		return
	}
	lastMsg := s.lastMessageTime()
	for _, projectID := range projects {
		dc := s.resolveProject(projectID).Dreaming
		if !dc.Enabled {
			continue
		}
		interval := dc.IntervalDuration()
		idle := time.Duration(dc.OnIdleSeconds) * time.Second

		s.lastScheduledDreamAtMu.Lock()
		lastDream := s.lastScheduledDreamAt[projectID]
		s.lastScheduledDreamAtMu.Unlock()

		if !s.shouldDreamProject(now, lastDream, lastMsg, interval, idle) {
			continue
		}
		// Advance the timestamp BEFORE dispatching. Two reasons:
		//
		//  1. If Dream returns Skipped (dreamMu held by a long pass),
		//     leaving the timestamp alone causes the scheduler to
		//     re-dispatch this project on every tick — `O(projects ×
		//     ticks)` goroutines piling up on TryLock during a multi-
		//     minute dream pass. Advancing here drains that storm.
		//
		//  2. The "intent to dream at `now`" is recorded regardless of
		//     whether the lock-holder happened to be us. Next interval
		//     fires cleanly from this point.
		//
		// Trade-off: an interval is "lost" when Dream is skipped. The
		// idle trigger picks up the slack — if buffered messages are
		// still there, the next tick will refire via the idle path.
		s.lastScheduledDreamAtMu.Lock()
		s.lastScheduledDreamAt[projectID] = now
		s.lastScheduledDreamAtMu.Unlock()
		s.runScheduledDream(ctx, projectID, dc)
	}
}

// shouldDreamProject answers "is this specific project due?" — interval
// elapsed since its own last scheduled pass, OR idle threshold met
// since the last record_messages came in for ANY project. The idle
// signal is global because record_messages stamps a single timestamp;
// that's a deliberate trade — a busy session in project A counts as
// "activity" against project B's idle clock too. In practice this is
// fine: idle = "things are quiet, dream now", and one quiet system
// is one quiet system.
//
// lastDream may be the zero value (first ever pass for this project);
// in that case the interval branch fires immediately so a freshly-
// buffering project gets dreamt on the next tick.
func (s *Server) shouldDreamProject(now, lastDream, lastMsg time.Time, interval, idle time.Duration) bool {
	if interval > 0 && now.Sub(lastDream) >= interval {
		return true
	}
	if idle <= 0 {
		return false
	}
	if lastMsg.IsZero() || now.Sub(lastMsg) < idle {
		return false
	}
	// Pending presence is already implied — scheduleTick only iterates
	// projects with buffered messages, so reaching here means there's
	// work to do.
	return true
}

// runScheduledDream runs one project's pass with its effective config.
// Returns true if a real pass executed — false if Dream returned Skipped
// (single-flight collision with a concurrent dream_now or longer-running
// prior pass) or errored. The scheduler uses the bool to decide whether
// to advance lastScheduledDreamAt: skipped passes leave the timestamp
// alone so the project gets reconsidered on the next tick.
func (s *Server) runScheduledDream(ctx context.Context, projectID string, dc config.DreamingConfig) bool {
	res, err := s.Dream(ctx, DreamOptions{
		ProjectID:       projectID,
		MinMessages:     dc.MinMessages,
		ContextMemories: dc.ContextMemories,
	})
	if err != nil {
		s.logger.Error("scheduled dream failed", "project_id", projectID, "err", err.Error())
		return false
	}
	if res.Skipped {
		return false
	}
	if res.SessionsProcessed == 0 {
		// No sessions matched (e.g. MinMessages filtered them all out).
		// Still counts as a real pass — the work was considered. Advance
		// the timestamp so we don't tight-loop reconsidering an empty
		// buffer.
		return true
	}
	s.logger.Info("scheduled dream completed",
		"project_id", projectID,
		"sessions", res.SessionsProcessed,
		"messages", res.MessagesProcessed,
		"created", res.OpsCreated,
		"updated", res.OpsUpdated,
		"deleted", res.OpsDeleted,
		"skipped_ops", res.OpsSkipped,
	)
	return true
}

// dreamOp is one operation in the reasoner's structured JSON response.
//
// Category names the content-shape axis the memory serves — one of the
// five canonical store.MemoryCategory values. Required on create ops;
// optional on update ops (when set, overwrites the existing category).
// Invalid / missing categories on create get stored as NULL and the
// memory surfaces in the Review queue for the user to classify.
type dreamOp struct {
	Op        string    `json:"op"`
	UUID      string    `json:"uuid,omitempty"`
	Content   string    `json:"content,omitempty"`
	Tags      *[]string `json:"tags,omitempty"`
	Category  string    `json:"category,omitempty"`
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

// The dream system prompt now lives in internal/config — defaulted from
// DefaultDreamPrompt, overridable per-user via the Settings UI which
// writes ~/.config/yullu/dream_prompt.txt. callReasoner above resolves
// the current value at call time so edits take effect on the next pass
// without a restart.

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

func buildDreamUserPrompt(projectID string, memories []store.Memory, msgs []store.SessionMessage, rejected []store.RejectedMemory) string {
	var buf strings.Builder
	// Stamp the active project up front so the reasoner has an explicit
	// boundary to reject cross-project facts against. Belt-and-braces with
	// the project filter on SessionMessages — the prompt-side guard means
	// even if a stray message slips through, the model has been told to
	// drop it instead of summarising it into a memory.
	fmt.Fprintf(&buf, "PROJECT: %s\n\n", projectID)
	buf.WriteString("CURRENT MEMORIES:\n")
	if len(memories) == 0 {
		buf.WriteString("(none yet)\n")
	} else {
		for _, m := range memories {
			fmt.Fprintf(&buf, "- uuid=%s tags=%v\n  %s\n", m.UUID, m.Tags, m.Content)
		}
	}

	// Anti-examples — memories a human marked as low-value with reasons.
	// Shown verbatim so the model can see what shape of memory not to
	// produce. Project-scoped; we never replay another project's signal
	// here.
	if len(rejected) > 0 {
		buf.WriteString("\nPREVIOUSLY REJECTED MEMORIES — a human marked these as low-value. Do NOT produce memories like these; take the rating comment as concrete guidance:\n")
		for _, r := range rejected {
			fmt.Fprintf(&buf, "- rating=%d/10 \"%s\"\n", r.Rating, r.Content)
			if r.Comment != "" {
				fmt.Fprintf(&buf, "  reason: %s\n", r.Comment)
			}
		}
	}

	buf.WriteString("\nCONVERSATION:\n")
	for _, m := range msgs {
		fmt.Fprintf(&buf, "[%s] %s\n", m.Role, m.Content)
	}
	return buf.String()
}
