package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tomanagle/yullu/cmd/internal/ai"
	"github.com/tomanagle/yullu/cmd/internal/applog"
	"github.com/tomanagle/yullu/cmd/internal/config"
	"github.com/tomanagle/yullu/cmd/internal/scope"
	"github.com/tomanagle/yullu/cmd/internal/server"
	"github.com/tomanagle/yullu/cmd/internal/store"
)

// App is the application state shared between the REST handlers (api.go)
// and the MCP transport. The exported methods are the same surface the
// frontend talks to over /api/*.
type App struct {
	ctx       context.Context
	logger    *slog.Logger
	cfgPath   string
	cfg       config.Config
	store     *store.Store
	embedder  ai.Embedder
	srv       *server.Server // dream/reconcile entry point; nil until openStore succeeds
	initError string         // last reason openStore failed, surfaced via Status()

	// mcpHandler is the http.Handler currently routing /mcp requests. It's
	// swapped each time openStore succeeds so SaveConfig (which can change
	// the embedder/reasoner) takes effect without restarting the listener.
	// Reads happen on every /mcp request; writes only on store re-open.
	mcpMu      sync.RWMutex
	mcpHandler http.Handler
}

// NewApp constructs an empty App. Wails calls OnStartup once the window is
// ready; we defer side-effecting work to that point so the constructor stays
// cheap.
func NewApp() *App {
	return &App{logger: applog.New().With("component", "desktop")}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfgPath = config.MustDefaultPath()
	a.cfg = config.MustLoad(a.cfgPath)
	a.openStore()
}

// swapMCPHandler is called by openStore whenever the underlying *Server
// changes (initial boot + after SaveConfig). The mux entry mounted at /mcp
// always reads the current handler under mcpMu, so requests in flight
// during a swap return cleanly via the old handler and subsequent ones
// hit the new one.
func (a *App) swapMCPHandler() {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	if a.srv == nil {
		a.mcpHandler = nil
		return
	}
	a.mcpHandler = a.srv.MCPHandler()
}

// mcpHandlerHTTP returns the http.Handler currently serving /mcp. nil if
// the store hasn't opened yet - main.go falls back to a 503 in that case.
func (a *App) mcpHandlerHTTP() http.Handler {
	a.mcpMu.RLock()
	defer a.mcpMu.RUnlock()
	return a.mcpHandler
}

func (a *App) shutdown(_ context.Context) {
	if a.store != nil {
		_ = a.store.Close()
	}
}

// openStore tries to build an embedder from the current config and open the
// SQLite store. Failures are captured on initError and logged; the UI
// surfaces both via Status() so the user can fix the cause without
// hunting through terminal output.
func (a *App) openStore() {
	a.initError = ""
	embedder, err := ai.BuildEmbedder(a.cfg, ai.NopRecorder())
	if err != nil {
		a.logger.Warn("embedder unavailable", "err", err.Error())
		a.initError = err.Error()
		return
	}
	dim := embedder.Dim()
	if dim <= 0 {
		msg := fmt.Sprintf("could not determine embedding dimension for %s - check provider connectivity", embedder.ID())
		a.logger.Warn(msg)
		a.initError = msg
		return
	}
	dbPath := store.MustDefaultDBPath()
	st, err := store.Open(dbPath, embedder.ID(), dim)
	if err != nil {
		a.logger.Warn("open store", "db_path", dbPath, "err", err.Error())
		a.initError = err.Error()
		return
	}
	a.store = st
	a.embedder = embedder
	a.logger.Info("store opened",
		"db_path", dbPath,
		"embedder", embedder.ID(),
		"embedder_dim", dim,
	)

	// Build a Server so the desktop can drive Dream / Reconcile / writes from
	// the UI using the same code paths the CLI uses. A nil reasoner is OK -
	// Dream will surface a clear error if no direct provider is configured.
	//
	// Sync is left at whatever the user configured. The Server's writer gets
	// scoped to scope.GitRoot(cwd) - when launched via `make start`, that
	// resolves to the yullu repo itself. Edits to memories from
	// other projects therefore land in this repo's .yullu/, which is a
	// known v1 limitation (the desktop is single-CWD; multi-project edits
	// should flow through the CLI in the relevant project).
	reasoner, _ := ai.BuildReasoner(a.cfg, ai.NopRecorder())
	a.srv = server.New(st, embedder, reasoner, a.cfg.Sync, a.cfg.Dreaming, a.logger)

	// Point the /mcp mux entry at this fresh Server. SaveConfig re-enters
	// this path with a new *Server (and a closed previous store) - the swap
	// ensures Claude Code's next call hits the live one.
	a.swapMCPHandler()

	// Pull events from .yullu/events/ into the local DB so memories the
	// user committed (or that teammates pushed) show up immediately on
	// launch. This is the same one-shot reconcile the CLI runs.
	if a.cfg.Sync.Enabled && a.cfg.Sync.AutoReconcileOnStartup {
		a.srv.LogReconcile(a.ctx)
	}
}

// Status reports whether the desktop app is ready to show memories. The
// frontend uses this to decide between the setup screen and the main UI.
// Hint, when set, is actionable next-step text for the user.
type Status struct {
	Ready      bool   `json:"ready"`
	ConfigPath string `json:"config_path"`
	DBPath     string `json:"db_path"`
	Embedder   string `json:"embedder,omitempty"`
	Message    string `json:"message,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

func (a *App) Status() Status {
	s := Status{
		ConfigPath: a.cfgPath,
		DBPath:     store.MustDefaultDBPath(),
	}
	if a.store != nil {
		s.Ready = true
		s.Embedder = a.cfg.Embedding.Provider
		return s
	}
	if a.initError != "" {
		s.Message = a.initError
		s.Hint = hintFor(a.initError)
		return s
	}
	if a.cfg.Voyage.APIKey == "" && a.cfg.OpenAI.APIKey == "" {
		s.Message = "No API key configured."
		s.Hint = "Add a Voyage or OpenAI key in Settings (free Voyage tier at voyageai.com)."
	} else {
		s.Message = "Couldn't open the memory store."
		s.Hint = "Check the terminal logs for details."
	}
	return s
}

// hintFor returns an actionable next step for known error shapes. Falls
// back to a generic pointer at the logs.
func hintFor(errMsg string) string {
	dbPath := store.MustDefaultDBPath()
	switch {
	case strings.Contains(errMsg, "embedding dimension mismatch"),
		strings.Contains(errMsg, "embedding model mismatch"):
		return "Your local DB was created with a different embedder. Delete " +
			dbPath + " (events in .yullu/ survive) or switch back to the previous embedder."
	case strings.Contains(errMsg, "no API key"):
		return "Add an API key in Settings."
	case strings.Contains(errMsg, "could not determine embedding dimension"):
		return "The embedder couldn't connect to its provider. Check the API key and network."
	case strings.Contains(errMsg, "open database"):
		return "Couldn't open the SQLite database at " + dbPath +
			". Make sure the directory exists and is writable. Try `mkdir -p " +
			filepath.Dir(dbPath) + "` then click Retry."
	case strings.Contains(errMsg, "sqlite-vec extension not loaded"):
		return "The binary was built without sqlite-vec. Rebuild from a clean checkout with `make build` (CGo is required)."
	default:
		return "Check the terminal logs for details."
	}
}

// ConfigView is the JSON-friendly projection of config.Config used by the
// frontend. Keeping it flat sidesteps Wails' generic struct-nesting quirks.
type ConfigView struct {
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	ReasoningProvider string `json:"reasoning_provider"`
	ReasoningModel    string `json:"reasoning_model"`
	VoyageAPIKey      string `json:"voyage_api_key"`
	OpenAIAPIKey      string `json:"openai_api_key"`
	AnthropicAPIKey   string `json:"anthropic_api_key"`
	SyncEnabled       bool   `json:"sync_enabled"`

	DreamingEnabled         bool   `json:"dreaming_enabled"`
	DreamingInterval        string `json:"dreaming_interval"`
	DreamingMinMessages     int    `json:"dreaming_min_messages"`
	DreamingContextMemories int    `json:"dreaming_context_memories"`
	DreamingOnIdleSeconds   int    `json:"dreaming_on_idle_seconds"`
}

func (a *App) GetConfig() ConfigView {
	return ConfigView{
		EmbeddingProvider:       a.cfg.Embedding.Provider,
		EmbeddingModel:          a.cfg.Embedding.Model,
		ReasoningProvider:       a.cfg.Reasoning.Provider,
		ReasoningModel:          a.cfg.Reasoning.Model,
		VoyageAPIKey:            a.cfg.Voyage.APIKey,
		OpenAIAPIKey:            a.cfg.OpenAI.APIKey,
		AnthropicAPIKey:         a.cfg.Anthropic.APIKey,
		SyncEnabled:             a.cfg.Sync.Enabled,
		DreamingEnabled:         a.cfg.Dreaming.Enabled,
		DreamingInterval:        a.cfg.Dreaming.Interval,
		DreamingMinMessages:     a.cfg.Dreaming.MinMessages,
		DreamingContextMemories: a.cfg.Dreaming.ContextMemories,
		DreamingOnIdleSeconds:   a.cfg.Dreaming.OnIdleSeconds,
	}
}

// SaveConfig overwrites the on-disk config.toml with the supplied view and
// re-opens the store so changes take effect immediately. Returns the new
// Status so the UI can refresh.
func (a *App) SaveConfig(v ConfigView) (Status, error) {
	a.cfg.Embedding.Provider = v.EmbeddingProvider
	a.cfg.Embedding.Model = v.EmbeddingModel
	a.cfg.Reasoning.Provider = v.ReasoningProvider
	a.cfg.Reasoning.Model = v.ReasoningModel
	a.cfg.Voyage.APIKey = v.VoyageAPIKey
	a.cfg.OpenAI.APIKey = v.OpenAIAPIKey
	a.cfg.Anthropic.APIKey = v.AnthropicAPIKey
	a.cfg.Sync.Enabled = v.SyncEnabled

	a.cfg.Dreaming.Enabled = v.DreamingEnabled
	a.cfg.Dreaming.Interval = v.DreamingInterval
	a.cfg.Dreaming.MinMessages = v.DreamingMinMessages
	a.cfg.Dreaming.ContextMemories = v.DreamingContextMemories
	a.cfg.Dreaming.OnIdleSeconds = v.DreamingOnIdleSeconds

	if err := writeConfigTOML(a.cfgPath, a.cfg); err != nil {
		return a.Status(), fmt.Errorf("write config: %w", err)
	}
	// Re-open the store with whatever the new embedder is.
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	a.openStore()
	return a.Status(), nil
}

// SessionStats is the dream-buffer summary surfaced on the Dreaming page.
type SessionStats struct {
	ProjectID string `json:"project_id"`
	Sessions  int    `json:"sessions"`
	Messages  int    `json:"messages"`
}

// GetSessionStats returns how many sessions/messages are queued ahead of a
// dream pass for the given projectID. Pass an empty string to use the CWD's
// resolved project (the desktop's own repo when run from this codebase).
func (a *App) GetSessionStats(projectID string) (SessionStats, error) {
	if a.store == nil {
		return SessionStats{}, fmt.Errorf("store not open")
	}
	if projectID == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return SessionStats{}, err
		}
		projectID, err = scope.Resolve(cwd)
		if err != nil {
			return SessionStats{}, err
		}
	}
	sessions, messages, err := a.store.CountSessionMessages(a.ctx, projectID)
	if err != nil {
		return SessionStats{}, err
	}
	return SessionStats{ProjectID: projectID, Sessions: sessions, Messages: messages}, nil
}

// Dream triggers a manual dream pass for the given project. Empty projectID
// uses the CWD's resolved project. Returns the per-session breakdown.
//
// Heads up: dreaming from the desktop requires a direct reasoner (Anthropic
// or OpenAI with an API key). MCP sampling can't be used here because there
// is no client session - the desktop app is the user, not the client.
func (a *App) Dream(projectID string) (*server.DreamResult, error) {
	if a.srv == nil {
		return nil, fmt.Errorf("desktop not initialised - finish setup first")
	}
	return a.srv.Dream(a.ctx, server.DreamOptions{
		ProjectID:       projectID,
		ContextMemories: a.cfg.Dreaming.ContextMemories,
	})
}

// ListProjects returns every project_id known to the local DB.
func (a *App) ListProjects() ([]string, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	return a.store.ListProjects(a.ctx)
}

// ListMemories returns memories sorted newest-first. Empty projectID means
// "every project" - the desktop UI uses that as its default view and uses
// the dropdown as an optional filter.
func (a *App) ListMemories(projectID string, limit int) ([]store.Memory, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if limit <= 0 {
		limit = 100
	}

	a.logger.Info("listing memories", "project_id", projectID, "limit", limit)
	value, err := a.store.List(a.ctx, projectID, limit)
	if err != nil {
		return nil, err
	}
	a.logger.Info("memories listed", "count", len(value))
	return value, nil
}

// GetMemoryGraph returns nodes + links describing how memories relate to
// each other (shared tags + embedding similarity). Empty projectID spans
// every project. Used by the /graph route in the desktop app.
func (a *App) GetMemoryGraph(projectID string) (store.MemoryGraph, error) {
	if a.store == nil {
		return store.MemoryGraph{}, fmt.Errorf("store not open")
	}
	return a.store.MemoryGraph(a.ctx, projectID)
}

// GetMemoryEventsByDay returns the daily memory-event counts for the last
// `days` calendar days, oldest first. Used by the chart on /stats.
func (a *App) GetMemoryEventsByDay(projectID string, days int) ([]store.DailyMemoryEvents, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return a.store.MemoryEventsByDay(a.ctx, projectID, days)
}

// GetUsageByDay returns the daily LLM usage totals for the last `days`
// calendar days, oldest first. Cost is in USD microcents (divide by 10^8
// for dollars). Spans every project.
func (a *App) GetUsageByDay(days int) ([]store.DailyUsage, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return a.store.UsageByDay(a.ctx, days)
}

// GetUsageSummary returns per-(provider, model, kind) totals over a window.
// sinceHours <= 0 means all-time. Used by the model breakdown chart.
func (a *App) GetUsageSummary(sinceHours int) ([]store.UsageBucket, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	since := time.Time{}
	if sinceHours > 0 {
		since = time.Now().Add(-time.Duration(sinceHours) * time.Hour)
	}
	return a.store.UsageSummary(a.ctx, since)
}

// GetMemoryStats returns aggregated lifecycle counts (created / updated /
// deleted / recalled) for projectID, plus the most-frequently-recalled
// memories. Empty projectID spans every project.
func (a *App) GetMemoryStats(projectID string) (store.MemoryStats, error) {
	if a.store == nil {
		return store.MemoryStats{}, fmt.Errorf("store not open")
	}
	return a.store.GetMemoryStats(a.ctx, projectID)
}

// SearchMemories runs a free, local, BM25-ranked full-text search across
// stored memories. Empty query falls back to ListMemories (newest-first);
// non-empty queries return results ordered by relevance. Empty projectID
// searches every project.
func (a *App) SearchMemories(projectID, query string, limit int) ([]store.Memory, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if limit <= 0 {
		limit = 100
	}
	if strings.TrimSpace(query) == "" {
		return a.store.List(a.ctx, projectID, limit)
	}
	return a.store.SearchText(a.ctx, projectID, query, limit)
}

// Retry re-runs openStore. Used by the setup card after the user fixes
// the underlying cause (deletes a stale DB, pastes an API key, creates the
// data directory, etc.). Returns the fresh Status so the UI can update.
func (a *App) Retry() Status {
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	a.embedder = nil
	a.openStore()
	return a.Status()
}

// UpdateMemory patches a memory's content and/or tags. Re-embeds when
// content actually changes so vector search stays accurate. Always
// overwrites tags with the supplied slice (treat empty as "clear").
//
// Heads up: this writes to the local DB only. Desktop-initiated changes
// don't currently propagate to .yullu/events/ - use Claude/Codex to
// `update_memory` inside the relevant project if you need teammates to see
// the edit.
func (a *App) UpdateMemory(id int64, content string, tags []string) (*store.Memory, error) {
	if a.store == nil || a.embedder == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	existing, err := a.store.Get(a.ctx, id)
	if err != nil {
		return nil, err
	}

	var contentPtr *string
	var newVec []float32
	if content != existing.Content {
		c := content
		contentPtr = &c
		vecs, err := a.embedder.Embed(a.ctx, []string{c})
		if err != nil {
			return nil, fmt.Errorf("embed: %w", err)
		}
		newVec = vecs[0]
	}
	tagsPtr := &tags

	if err := a.store.Update(a.ctx, id, contentPtr, tagsPtr, newVec); err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return a.store.Get(a.ctx, id)
}

// DeleteMemory removes a memory by local int ID.
func (a *App) DeleteMemory(id int64) error {
	if a.store == nil {
		return fmt.Errorf("store not open")
	}
	return a.store.Delete(a.ctx, id)
}
