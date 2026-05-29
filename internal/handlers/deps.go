package handlers

import (
	"context"
	"time"

	"github.com/tomanagle/yullu/internal/server"
	"github.com/tomanagle/yullu/internal/store"
)

// Consumer-defined interfaces: each handler depends on the smallest interface
// that satisfies it. The concrete *store.Store, *server.Server, and *main.App
// each satisfy several of these - composition happens at the call site (main.go).
//
// Naming convention: <Domain><Verb>er for plain CRUD, <Domain>Service for
// state-machine ops that coordinate multiple subsystems.

// StatusService coordinates the embedder + store + reasoner lifecycle and
// surfaces it as the JSON Status the UI uses to gate the setup screen.
// Retry re-runs the open-store path after the user fixes a problem.
type StatusService interface {
	Status() Status
	Retry() Status
}

// ConfigService reads and persists the user-facing config view. SaveConfig
// returns the new Status so the UI can refresh in one round-trip.
type ConfigService interface {
	GetConfig() ConfigView
	SaveConfig(v ConfigView) (Status, error)
}

// MemoryReader is plain read access to stored memories. Satisfied directly
// by *store.Store.
type MemoryReader interface {
	List(ctx context.Context, projectID string, limit int) ([]store.Memory, error)
	SearchText(ctx context.Context, projectID, query string, limit int) ([]store.Memory, error)
}

// MemoryRecaller embeds a natural-language query and runs the same
// vector-similarity search the MCP retrieve_memories tool uses, with
// optional category filtering. Used by the UserPromptSubmit hook to
// pull task-relevant memories before the agent sees the user's prompt.
// Lives on App because it needs both the embedder and the store.
type MemoryRecaller interface {
	RecallMemories(ctx context.Context, projectID, query string, categories []store.MemoryCategory, limit int) ([]store.Memory, error)
}

// MemoryEditor handles update + delete, which the App coordinates because
// updating content also requires a re-embed via the embedder.
type MemoryEditor interface {
	UpdateMemory(ctx context.Context, id int64, content string, tags []string) (*store.Memory, error)
	DeleteMemory(ctx context.Context, id int64) error
}

// MemoryRater handles user-supplied quality ratings for the review queue.
// ListUnrated returns memories the user hasn't scored yet (newest first).
// RateMemory writes a rating (1..10) + optional comment; ratings ≤ 5
// move the memory into the rejected-memories anti-example table and
// delete the original row — the returned *Memory is nil in that case.
type MemoryRater interface {
	ListUnrated(ctx context.Context, projectID string, limit int) ([]store.Memory, error)
	RateMemory(ctx context.Context, id int64, rating int, comment string) (*store.Memory, error)
}

// ProjectLister enumerates distinct project_ids known to the store.
type ProjectLister interface {
	ListProjects(ctx context.Context) ([]string, error)
}

// GraphReader returns the nodes/links used by the Graph page. The store
// computes shared-tag and embedding-similarity edges in one shot.
type GraphReader interface {
	MemoryGraph(ctx context.Context, projectID string) (store.MemoryGraph, error)
}

// MemoryStatsReader aggregates lifecycle counts and per-day events.
type MemoryStatsReader interface {
	GetMemoryStats(ctx context.Context, projectID string) (store.MemoryStats, error)
	MemoryEventsByDay(ctx context.Context, projectID string, days int) ([]store.DailyMemoryEvents, error)
}

// UsageReader exposes provider/model usage aggregates by day or window.
type UsageReader interface {
	UsageByDay(ctx context.Context, days int) ([]store.DailyUsage, error)
	UsageSummary(ctx context.Context, since time.Time) ([]store.UsageBucket, error)
}

// SessionStatsProvider counts the unprocessed dream-buffer messages for
// the requested project (CWD's project when projectID is empty).
type SessionStatsProvider interface {
	GetSessionStats(ctx context.Context, projectID string) (SessionStats, error)
}

// DreamStatsReader aggregates persisted dream-pass results over a window.
// Powers the "dreaming activity" section of the Stats dashboard.
type DreamStatsReader interface {
	DreamStats(ctx context.Context, projectID string, days int) (store.DreamStats, error)
}

// ProjectOverridesService reads + writes the two-layer per-project override
// files. Repo layer is committed (.yullu/config.toml); user layer is
// private ($XDG_CONFIG_HOME/yullu/projects/<id>.toml). The App
// coordinates both because layer-routing depends on which fields the UI
// posted.
type ProjectOverridesService interface {
	GetProjectOverrides(ctx context.Context, projectID string) (ProjectOverridesView, error)
	SaveProjectOverrides(ctx context.Context, projectID string, repo, user ProjectOverridePayload) (ProjectOverridesView, error)
}

// MessageRecorder is the non-MCP entry point into the session_messages
// buffer. Used by POST /api/messages so external hooks (Claude Code Stop
// hook, etc.) can append turns without speaking MCP JSON-RPC. Returns the
// resolved project_id so the client can verify which scope the messages
// landed in.
type MessageRecorder interface {
	RecordMessages(
		ctx context.Context,
		projectOverride, callerCwd, sessionID string,
		msgs []RecordedMessage,
	) (string, error)
}

// SessionBufferReader returns the buffered (un-dreamed) session
// messages for one project, grouped by session_id. Powers the Dreaming
// page's "Buffered sessions" card.
type SessionBufferReader interface {
	BufferedSessions(ctx context.Context, projectID string) ([]BufferedSession, error)
}

// BufferedSession is one session's worth of pending messages.
// MessageCount is the total in the buffer; Messages may be a trimmed
// subset if we ever paginate, but for now it's the full set.
type BufferedSession struct {
	SessionID    string             `json:"session_id"`
	ProjectID    string             `json:"project_id"`
	MessageCount int                `json:"message_count"`
	Messages     []BufferedMessage  `json:"messages"`
}

// BufferedMessage is one user/assistant turn waiting for the dreamer.
type BufferedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	At      string `json:"at"` // RFC3339; absent => never recorded (shouldn't happen)
}

// DreamPassLister returns the per-cycle dream history shown on the
// Stats page (one row per non-skipped pass, newest first).
type DreamPassLister interface {
	ListDreamPasses(ctx context.Context, projectID string, limit int) ([]store.DreamPass, error)
}

// DreamProgressService exposes the live progress of the in-flight
// dream pass (or the last finished pass when nothing is running). The
// UI polls this to render a "Dreaming…" indicator with phase, counters,
// and the session being reasoned about right now.
type DreamProgressService interface {
	DreamProgress() DreamProgressView
}

// DreamProgressView is the GET response shape for /api/dream/progress.
// All counters reset to zero at the start of each pass; Running flips
// false the moment the pass returns, so the FE can show "last pass
// finished at X" without making a second call.
type DreamProgressView struct {
	Running           bool   `json:"running"`
	ProjectID         string `json:"project_id,omitempty"`
	Phase             string `json:"phase,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`  // RFC3339
	FinishedAt        string `json:"finished_at,omitempty"` // RFC3339
	TotalSessions     int    `json:"total_sessions"`
	CompletedSessions int    `json:"completed_sessions"`
	CurrentSessionID  string `json:"current_session_id,omitempty"`
	MessagesProcessed int    `json:"messages_processed"`
	OpsCreated        int    `json:"ops_created"`
	OpsUpdated        int    `json:"ops_updated"`
	OpsDeleted        int    `json:"ops_deleted"`
	OpsSkipped        int    `json:"ops_skipped"`
	LastError         string `json:"last_error,omitempty"`

	// Scheduler-derived. SchedulerEnabled is false when [dreaming].enabled
	// is off in the effective config — in that case the *At fields stay
	// empty and the UI should show "manual only".
	SchedulerEnabled bool   `json:"scheduler_enabled"`
	IntervalSeconds  int    `json:"interval_seconds"`
	OnIdleSeconds    int    `json:"on_idle_seconds"`
	LastMessageAt    string `json:"last_message_at,omitempty"`   // RFC3339
	LastScheduledAt  string `json:"last_scheduled_at,omitempty"` // RFC3339
	NextIntervalAt   string `json:"next_interval_at,omitempty"`  // RFC3339
	NextIdleAt       string `json:"next_idle_at,omitempty"`      // RFC3339
	NextAt           string `json:"next_at,omitempty"`           // RFC3339, soonest of the two
	NextReason       string `json:"next_reason,omitempty"`       // "interval" | "idle"
}

// DreamPromptService reads and writes the user-customisable dream
// system prompt. Empty Save body resets to the built-in default. Get
// returns the current text plus the default so the UI can show a
// "reset" affordance and a diff hint.
type DreamPromptService interface {
	GetDreamPrompt() DreamPromptView
	SaveDreamPrompt(text string) (DreamPromptView, error)
}

// DreamPromptView is the GET response shape. IsCustom is true when the
// prompt comes from the user's override file (vs the compiled-in
// default). OutputFormat is the locked JSON-contract suffix the server
// always appends before calling the reasoner — surfaced read-only so
// the UI can show "this is what gets bolted on" without letting the
// user break parseDreamResponse by deleting it.
type DreamPromptView struct {
	Prompt       string `json:"prompt"`
	Default      string `json:"default"`
	OutputFormat string `json:"output_format"`
	IsCustom     bool   `json:"is_custom"`
	Path         string `json:"path,omitempty"`
}

// RecordedMessage is one user/assistant turn. role must be "user" or
// "assistant"; content is the rendered text of the turn.
type RecordedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Dreamer runs a single dream pass - extracting durable memories from the
// recorded session_messages buffer. The App's Dream method is what wires
// in the configured ContextMemories and reasoner.
type Dreamer interface {
	Dream(ctx context.Context, opts server.DreamOptions) (*server.DreamResult, error)
}
