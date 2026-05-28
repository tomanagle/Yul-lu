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

// MemoryEditor handles update + delete, which the App coordinates because
// updating content also requires a re-embed via the embedder.
type MemoryEditor interface {
	UpdateMemory(ctx context.Context, id int64, content string, tags []string) (*store.Memory, error)
	DeleteMemory(ctx context.Context, id int64) error
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
		projectOverride, sessionID string,
		msgs []RecordedMessage,
	) (string, error)
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
