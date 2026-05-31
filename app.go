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

	"github.com/tomanagle/yullu/internal/ai"
	"github.com/tomanagle/yullu/internal/applog"
	"github.com/tomanagle/yullu/internal/config"
	"github.com/tomanagle/yullu/internal/handlers"
	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/server"
	"github.com/tomanagle/yullu/internal/store"
)

// App owns the runtime state (config, store, embedder, MCP server) and
// implements the interfaces in internal/handlers that the REST handlers
// depend on. State-machine methods (Status/Retry/SaveConfig) mutate the
// fields; read methods just delegate to the underlying store.
//
// Concurrency model: mu (RWMutex) guards every mutable field
// — cfg, store, embedder, srv, mcpHandler, initError. Readers acquire
// RLock just long enough to snapshot the pointer they need into a local,
// then release; they operate on the local for the rest of the handler.
// Writers (openStore, Retry, SaveConfig, shutdown) hold Lock for the
// duration of the swap.
//
// Why a snapshot pattern: handler methods do slow IO (DB queries,
// reasoner calls) — we never want to hold the App lock across them, or
// SaveConfig would block for minutes. The snapshot keeps lock windows
// at "copy three pointers" scale.
type App struct {
	ctx     context.Context
	logger  *slog.Logger
	cfgPath string

	mu         sync.RWMutex
	cfg        config.Config
	store      *store.Store
	embedder   ai.Embedder
	srv        *server.Server // dream/reconcile entry point; nil until openStore succeeds
	initError  string         // last reason openStore failed, surfaced via Status()
	mcpHandler http.Handler   // current /mcp router; swapped on each successful openStore
}

// appSnapshot is the captured pointer view a handler operates on. It is
// always a value copy of the pointer fields under a.mu — once a reader
// has the snapshot, it never re-touches a.* for state. The pointed-to
// store/srv/embedder may be swapped underneath by a concurrent writer,
// but the in-flight handler keeps a reference to the old generation
// and finishes safely.
type appSnapshot struct {
	store    *store.Store
	srv      *server.Server
	embedder ai.Embedder
	cfg      config.Config
}

// snapshot captures the App's mutable state under a brief RLock. Use
// this at the top of every public handler method; never read a.store /
// a.srv / a.embedder / a.cfg directly outside the locked region.
func (a *App) snapshot() appSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return appSnapshot{
		store:    a.store,
		srv:      a.srv,
		embedder: a.embedder,
		cfg:      a.cfg,
	}
}

func NewApp() *App {
	return &App{logger: applog.New().With("component", "desktop")}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfgPath = config.MustDefaultPath()
	cfg := config.MustLoad(a.cfgPath)
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	a.openStore()
}

func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		a.srv.StopScheduler()
	}
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
}

// mcpHandlerHTTP returns the current /mcp router. The mux entry
// dispatched via mcpProxy reads through this on every request, so a
// SaveConfig swap takes effect at the next request boundary while
// in-flight requests finish on the previous handler.
func (a *App) mcpHandlerHTTP() http.Handler {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mcpHandler
}

// openStore acquires a.mu and runs the open path. Use this from the
// public surface. Internal callers that already hold the lock should
// call openStoreLocked directly.
func (a *App) openStore() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.openStoreLocked()
}

// openStoreLocked builds an embedder from cfg, opens the SQLite store,
// and (re)constructs the MCP server. CALLER MUST HOLD a.mu.Lock().
// Failures are captured on initError and logged; the UI surfaces both
// via Status() so the user can fix the cause without hunting through
// terminal output.
func (a *App) openStoreLocked() {
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

	// Stop the previous server's scheduler before replacing it —
	// SaveConfig rebuilds the *Server, and without this stop call the
	// old scheduler keeps polling against the same store, racing with
	// the new one.
	if a.srv != nil {
		a.srv.StopScheduler()
	}

	reasoner, _ := ai.BuildReasoner(a.cfg, ai.NopRecorder())
	a.srv = server.New(st, embedder, reasoner, a.cfg, a.logger)
	// Inline what swapMCPHandler used to do — we already hold the lock.
	a.mcpHandler = a.srv.MCPHandler()

	// Start the background dream scheduler. Without this, no automatic
	// dream passes ever fire — buffered messages just accumulate until
	// someone clicks "Dream now" or an MCP client calls dream_now.
	// Previously only the legacy `yullu stdio` path called this; the
	// desktop HTTP server (the normal entry point) was missing it
	// entirely, so per-project schedules never fired no matter how the
	// user configured them.
	if a.ctx != nil {
		a.srv.StartScheduler(a.ctx)
	}

	if a.cfg.Sync.Enabled && a.cfg.Sync.AutoReconcileOnStartup {
		// LogReconcile is a long-running call; drop the lock for it. The
		// fields it reads (a.srv) are already captured by the method
		// receiver before this point, so it's safe.
		srv := a.srv
		go func() { srv.LogReconcile(a.ctx) }()
	}
}

// ---------- StatusService ----------

func (a *App) Status() handlers.Status {
	a.mu.RLock()
	cfg := a.cfg
	storeOpen := a.store != nil
	initErr := a.initError
	a.mu.RUnlock()

	s := handlers.Status{
		ConfigPath: a.cfgPath,
		DBPath:     store.MustDefaultDBPath(),
	}
	if storeOpen {
		s.Ready = true
		s.Embedder = cfg.Embedding.Provider
		// Reasoner is populated only when a direct provider is configured.
		// Empty signals "sampling-only mode" to the UI (no background
		// scheduler, no desktop button — the assistant must call dream_now).
		if cfg.Reasoning.Provider != "" {
			s.Reasoner = cfg.Reasoning.Provider
		}
		return s
	}
	if initErr != "" {
		s.Message = initErr
		s.Hint = hintFor(initErr)
		return s
	}
	if cfg.Voyage.APIKey == "" && cfg.OpenAI.APIKey == "" {
		s.Message = "No API key configured."
		s.Hint = "Add a Voyage or OpenAI key in Settings (free Voyage tier at voyageai.com)."
	} else {
		s.Message = "Couldn't open the memory store."
		s.Hint = "Check the terminal logs for details."
	}
	return s
}

// Retry re-runs openStore after the user fixes the underlying cause
// (deletes a stale DB, pastes an API key, creates the data directory).
// Holds the write lock for the duration so handlers don't catch a
// partially-swapped store mid-reset.
func (a *App) Retry() handlers.Status {
	a.mu.Lock()
	// Tear down the previous generation completely before openStoreLocked
	// runs. If it fails partway through (e.g. embedder build errors),
	// a.srv and a.mcpHandler MUST already be nil — otherwise they keep
	// pointing at the now-closed store and every /mcp request returns
	// "sql: database is closed" until app restart.
	if a.srv != nil {
		a.srv.StopScheduler()
		a.srv = nil
	}
	a.mcpHandler = nil
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	a.embedder = nil
	a.openStoreLocked()
	a.mu.Unlock()
	return a.Status()
}

// hintFor maps known error shapes to actionable next steps.
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

// ---------- ConfigService ----------

func (a *App) GetConfig() handlers.ConfigView {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	return handlers.ConfigView{
		EmbeddingProvider:       cfg.Embedding.Provider,
		EmbeddingModel:          cfg.Embedding.Model,
		ReasoningProvider:       cfg.Reasoning.Provider,
		ReasoningModel:          cfg.Reasoning.Model,
		VoyageAPIKey:            cfg.Voyage.APIKey,
		OpenAIAPIKey:            cfg.OpenAI.APIKey,
		AnthropicAPIKey:         cfg.Anthropic.APIKey,
		SyncEnabled:             cfg.Sync.Enabled,
		DreamingEnabled:         cfg.Dreaming.Enabled,
		DreamingInterval:        cfg.Dreaming.Interval,
		DreamingMinMessages:     cfg.Dreaming.MinMessages,
		DreamingContextMemories: cfg.Dreaming.ContextMemories,
		DreamingOnIdleSeconds:   cfg.Dreaming.OnIdleSeconds,
		RetrievalMinSimilarity:  cfg.Retrieval.MinSimilarity,
	}
}

func (a *App) SaveConfig(v handlers.ConfigView) (handlers.Status, error) {
	a.mu.Lock()
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
	a.cfg.Retrieval.MinSimilarity = v.RetrievalMinSimilarity

	cfgCopy := a.cfg
	cfgPath := a.cfgPath
	a.mu.Unlock()

	// Write the TOML outside the lock — it's filesystem IO and the
	// caller doesn't need to be serialised against unrelated readers
	// while the file syncs. The bytes we write are the cfgCopy snapshot,
	// so a concurrent SaveConfig (unlikely from the single-user UI) would
	// race file contents, which is acceptable.
	if err := writeConfigTOML(cfgPath, cfgCopy); err != nil {
		return a.Status(), fmt.Errorf("write config: %w", err)
	}

	// Re-open the store under the write lock so handlers in flight see
	// either the old or the new generation, never a half-swapped state.
	a.mu.Lock()
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	a.openStoreLocked()
	a.mu.Unlock()
	return a.Status(), nil
}

// ---------- MemoryReader ----------

func (a *App) List(ctx context.Context, projectID string, limit int) ([]store.Memory, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if limit <= 0 {
		limit = 100
	}
	return snap.store.List(ctx, projectID, limit)
}

func (a *App) SearchText(ctx context.Context, projectID, query string, limit int) ([]store.Memory, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if limit <= 0 {
		limit = 100
	}
	if strings.TrimSpace(query) == "" {
		return snap.store.List(ctx, projectID, limit)
	}
	return snap.store.SearchText(ctx, projectID, query, limit)
}

// ---------- MemoryEditor ----------

// UpdateMemory patches a memory's content and/or tags. Re-embeds when
// content actually changes so vector search stays accurate. Always
// overwrites tags with the supplied slice (treat empty as "clear").
//
// Heads up: this writes to the local DB only. Desktop-initiated changes
// don't currently propagate to .yullu/logs/.
func (a *App) UpdateMemory(ctx context.Context, id int64, content string, tags []string) (*store.Memory, error) {
	snap := a.snapshot()
	if snap.store == nil || snap.embedder == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	existing, err := snap.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	var contentPtr *string
	var newVec []float32
	if content != existing.Content {
		c := content
		contentPtr = &c
		vecs, err := snap.embedder.Embed(ctx, []string{c})
		if err != nil {
			return nil, fmt.Errorf("embed: %w", err)
		}
		newVec = vecs[0]
	}
	tagsPtr := &tags

	if err := snap.store.Update(ctx, id, contentPtr, tagsPtr, newVec); err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return snap.store.Get(ctx, id)
}

func (a *App) DeleteMemory(ctx context.Context, id int64) error {
	snap := a.snapshot()
	if snap.store == nil {
		return fmt.Errorf("store not open")
	}
	return snap.store.Delete(ctx, id)
}

// ---------- ProjectLister ----------

func (a *App) ListProjects(ctx context.Context) ([]string, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	return snap.store.ListProjects(ctx)
}

// ---------- GraphReader ----------

func (a *App) MemoryGraph(ctx context.Context, projectID string) (store.MemoryGraph, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return store.MemoryGraph{}, fmt.Errorf("store not open")
	}
	return snap.store.MemoryGraph(ctx, projectID)
}

// ---------- MemoryStatsReader ----------

func (a *App) GetMemoryStats(ctx context.Context, projectID string) (store.MemoryStats, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return store.MemoryStats{}, fmt.Errorf("store not open")
	}
	return snap.store.GetMemoryStats(ctx, projectID)
}

func (a *App) MemoryEventsByDay(ctx context.Context, projectID string, days int) ([]store.DailyMemoryEvents, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return snap.store.MemoryEventsByDay(ctx, projectID, days)
}

// ---------- DreamStatsReader ----------

func (a *App) DreamStats(ctx context.Context, projectID string, days int) (store.DreamStats, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return store.DreamStats{}, fmt.Errorf("store not open")
	}
	return snap.store.DreamStats(ctx, projectID, days)
}

// ---------- UsageReader ----------

func (a *App) UsageByDay(ctx context.Context, days int) ([]store.DailyUsage, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return snap.store.UsageByDay(ctx, days)
}

func (a *App) UsageSummary(ctx context.Context, since time.Time) ([]store.UsageBucket, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return snap.store.UsageSummary(ctx, since)
}

// ---------- SessionStatsProvider ----------

func (a *App) GetSessionStats(ctx context.Context, projectID string) (handlers.SessionStats, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return handlers.SessionStats{}, fmt.Errorf("store not open")
	}
	if projectID == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return handlers.SessionStats{}, err
		}
		projectID, err = scope.Resolve(cwd)
		if err != nil {
			return handlers.SessionStats{}, err
		}
	}
	sessions, messages, err := snap.store.CountSessionMessages(ctx, projectID)
	if err != nil {
		return handlers.SessionStats{}, err
	}
	return handlers.SessionStats{ProjectID: projectID, Sessions: sessions, Messages: messages}, nil
}

// ---------- Dreamer ----------

// Dream triggers a manual dream pass. The handler passes ContextMemories
// (read from cfg at registration time) in opts.
//
// Heads up: dreaming from the desktop requires a direct reasoner (Anthropic
// or OpenAI with an API key). MCP sampling can't be used here because there
// is no client session - the desktop is the user, not the client.
func (a *App) Dream(ctx context.Context, opts server.DreamOptions) (*server.DreamResult, error) {
	snap := a.snapshot()
	if snap.srv == nil {
		return nil, fmt.Errorf("desktop not initialised - finish setup first")
	}
	return snap.srv.Dream(ctx, opts)
}

// ---------- MemoryRecaller ----------

// RecallMemories powers the UserPromptSubmit hook (and any other caller
// that wants "given this text, which memories are relevant?"). Embeds
// the query with the current embedder, runs vector search filtered to
// the requested categories, and returns the hits.
//
// Categories may be nil — that returns the unfiltered top-K. limit
// defaults to 5 when <= 0; we keep it conservative because every result
// adds to the agent's prompt-time tokens.
func (a *App) RecallMemories(ctx context.Context, projectID, query string, categories []store.MemoryCategory, limit int) ([]store.Memory, error) {
	snap := a.snapshot()
	if snap.store == nil || snap.embedder == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	vecs, err := snap.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors, expected 1", len(vecs))
	}
	var minSim float64
	if snap.srv != nil {
		minSim = snap.srv.RetrievalMinSimilarity(projectID)
	}
	return snap.store.Search(ctx, projectID, vecs[0], query, limit, categories, minSim)
}

// ---------- MemoryRater ----------

// ListUnrated returns memories awaiting a rating for projectID, newest
// first. Powers the dedicated Review page.
func (a *App) ListUnrated(ctx context.Context, projectID string, limit int) ([]store.Memory, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return snap.store.ListUnrated(ctx, projectID, limit)
}

// RateMemory writes a rating + comment. ≤ 5 moves the row to
// rejected_memories (returned *Memory will be nil to signal the FE the
// row is gone); ≥ 6 keeps it and returns the refreshed row.
func (a *App) RateMemory(ctx context.Context, id int64, rating int, comment string) (*store.Memory, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	if err := snap.store.RateMemory(ctx, id, rating, comment); err != nil {
		return nil, err
	}
	if rating <= 5 {
		// Rejection path — the row no longer exists.
		return nil, nil
	}
	return snap.store.Get(ctx, id)
}

// ---------- RetrievalAnalytics ----------

// ListRetrievals returns recent recall events (with the memory they
// returned + any verdict) for the Retrievals review surface.
func (a *App) ListRetrievals(ctx context.Context, projectID string, limit int) ([]store.RetrievalEvent, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return snap.store.ListRetrievals(ctx, projectID, limit)
}

// RateRetrieval records a developer's +1/-1 verdict on a single recall.
func (a *App) RateRetrieval(ctx context.Context, eventID int64, verdict int, comment string) error {
	snap := a.snapshot()
	if snap.store == nil {
		return fmt.Errorf("store not open")
	}
	return snap.store.RateRetrieval(ctx, eventID, verdict, comment)
}

// ---------- MessageRecorder ----------

// RecordMessages funnels into the server's session-buffer write path
// (same code the record_messages MCP tool uses), so REST-side hooks
// (e.g. `yullu record-turn` invoked by Claude Code's Stop hook) feed
// the dreamer just like MCP clients do.
func (a *App) RecordMessages(
	ctx context.Context,
	projectOverride, callerCwd, sessionID string,
	msgs []handlers.RecordedMessage,
) (string, error) {
	snap := a.snapshot()
	if snap.srv == nil {
		return "", fmt.Errorf("desktop not initialised - finish setup first")
	}
	inputs := make([]store.SessionMessageInput, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "" || m.Content == "" {
			continue // skip empties; the hook trims aggressively too
		}
		inputs = append(inputs, store.SessionMessageInput{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	if len(inputs) == 0 {
		// resolve project so the caller still gets a sensible echo.
		return projectOverride, nil
	}
	return snap.srv.RecordSessionMessages(ctx, projectOverride, callerCwd, sessionID, inputs)
}

// ---------- SessionBufferReader ----------

// BufferedSessions returns every session_id with pending messages for
// projectID, each paired with its full ordered message list. Empty
// projectID resolves to the CWD's project (mirrors RecordMessages).
func (a *App) BufferedSessions(ctx context.Context, projectID string) ([]handlers.BufferedSession, error) {
	snap := a.snapshot()
	if snap.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	if projectID == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		projectID, err = scope.Resolve(cwd)
		if err != nil {
			return nil, err
		}
	}
	sessionIDs, err := snap.store.SessionsWithMessages(ctx, projectID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.BufferedSession, 0, len(sessionIDs))
	for _, sid := range sessionIDs {
		msgs, err := snap.store.SessionMessages(ctx, projectID, sid)
		if err != nil {
			return nil, fmt.Errorf("load messages for %s: %w", sid, err)
		}
		viewMsgs := make([]handlers.BufferedMessage, 0, len(msgs))
		for _, m := range msgs {
			viewMsgs = append(viewMsgs, handlers.BufferedMessage{
				Role:    m.Role,
				Content: m.Content,
				At:      m.At.Format(time.RFC3339),
			})
		}
		out = append(out, handlers.BufferedSession{
			SessionID:    sid,
			ProjectID:    projectID,
			MessageCount: len(msgs),
			Messages:     viewMsgs,
		})
	}
	return out, nil
}

// ---------- DreamPromptService ----------

// GetDreamPrompt returns the active prompt plus the built-in default so
// the UI can render a "reset" affordance and a hint at what changed.
func (a *App) GetDreamPrompt() handlers.DreamPromptView {
	text, custom, err := config.LoadDreamPrompt()
	if err != nil {
		a.logger.Warn("load dream prompt", "err", err.Error())
	}
	return handlers.DreamPromptView{
		Prompt:       text,
		Default:      config.DefaultDreamPrompt,
		OutputFormat: config.DreamPromptOutputFormat,
		IsCustom:     custom,
		Path:         config.DreamPromptPath(),
	}
}

// SaveDreamPrompt persists a custom prompt (empty = reset to default).
// The next dream pass picks up the new text — no restart required —
// because dream.go reads the prompt at call time.
func (a *App) SaveDreamPrompt(text string) (handlers.DreamPromptView, error) {
	if err := config.WriteDreamPrompt(text); err != nil {
		return handlers.DreamPromptView{}, err
	}
	return a.GetDreamPrompt(), nil
}

// DreamContextMemories returns the current [dreaming].context_memories
// value under RLock. Bound into RegisterParams as a method value (a
// thunk) so the PostDream handler reads the live config on every
// request instead of caching the value at boot time.
func (a *App) DreamContextMemories() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Dreaming.ContextMemories
}

// ---------- DreamProgressService ----------

// DreamProgress translates the server-side in-memory snapshot into the
// JSON-friendly view the FE polls. Timestamps come out as RFC3339 (or
// empty string when zero) so the FE can pass them straight to Date()
// without a nullable-check.
func (a *App) DreamProgress() handlers.DreamProgressView {
	state := a.snapshot()
	if state.srv == nil {
		return handlers.DreamProgressView{}
	}
	snap := state.srv.DreamProgressSnapshot()
	v := handlers.DreamProgressView{
		Running:           snap.Running,
		ProjectID:         snap.ProjectID,
		Phase:             snap.Phase,
		TotalSessions:     snap.TotalSessions,
		CompletedSessions: snap.CompletedSessions,
		CurrentSessionID:  snap.CurrentSessionID,
		MessagesProcessed: snap.MessagesProcessed,
		OpsCreated:        snap.OpsCreated,
		OpsUpdated:        snap.OpsUpdated,
		OpsDeleted:        snap.OpsDeleted,
		OpsSkipped:        snap.OpsSkipped,
		LastError:         snap.LastError,
	}
	if !snap.StartedAt.IsZero() {
		v.StartedAt = snap.StartedAt.Format(time.RFC3339)
	}
	if !snap.FinishedAt.IsZero() {
		v.FinishedAt = snap.FinishedAt.Format(time.RFC3339)
	}
	v.SchedulerEnabled = snap.SchedulerEnabled
	v.IntervalSeconds = snap.IntervalSeconds
	v.OnIdleSeconds = snap.OnIdleSeconds
	if !snap.LastMessageAt.IsZero() {
		v.LastMessageAt = snap.LastMessageAt.Format(time.RFC3339)
	}
	if !snap.LastScheduledAt.IsZero() {
		v.LastScheduledAt = snap.LastScheduledAt.Format(time.RFC3339)
	}
	if !snap.NextIntervalAt.IsZero() {
		v.NextIntervalAt = snap.NextIntervalAt.Format(time.RFC3339)
	}
	if !snap.NextIdleAt.IsZero() {
		v.NextIdleAt = snap.NextIdleAt.Format(time.RFC3339)
	}
	if !snap.NextAt.IsZero() {
		v.NextAt = snap.NextAt.Format(time.RFC3339)
	}
	v.NextReason = snap.NextReason
	return v
}

// ---------- ProjectOverridesService ----------

// GetProjectOverrides reads the repo + user override files for projectID,
// stacks them onto the global config to compute the effective view, and
// returns all three so the UI can render inherited values as placeholders.
func (a *App) GetProjectOverrides(ctx context.Context, projectID string) (handlers.ProjectOverridesView, error) {
	snap := a.snapshot()
	cfg := snap.cfg

	repoOverride, repoWarn := readOverride(config.RepoOverridePath(a.projectGitRoot(ctx, projectID, snap.store)), false)
	userOverride, userWarn := readOverride(config.UserOverridePath(projectID), true)

	effective := config.Merge(cfg, repoOverride)
	effective = config.Merge(effective, userOverride)

	view := handlers.ProjectOverridesView{
		ProjectID: projectID,
		Repo:      configOverrideToPayload(repoOverride),
		User:      configOverrideToPayload(userOverride),
		Effective: effectiveFromConfig(effective),
	}
	view.Warnings = append(view.Warnings, repoWarn...)
	view.Warnings = append(view.Warnings, userWarn...)
	return view, nil
}

// SaveProjectOverrides writes both layers and returns the refreshed view.
// API keys are stripped from the repo payload before writing - committed
// files must not carry secrets. The Server's config cache for this project
// is invalidated so the next operation picks up the change.
func (a *App) SaveProjectOverrides(ctx context.Context, projectID string, repo, user handlers.ProjectOverridePayload) (handlers.ProjectOverridesView, error) {
	// Repo payload: drop API keys before persistence.
	repo.VoyageAPIKey = nil
	repo.OpenAIAPIKey = nil
	repo.AnthropicAPIKey = nil

	repoOverride := payloadToConfigOverride(repo)
	userOverride := payloadToConfigOverride(user)

	snap := a.snapshot()
	gitRoot := a.projectGitRoot(ctx, projectID, snap.store)
	if path := config.RepoOverridePath(gitRoot); path != "" {
		if err := config.WriteOverride(path, repoOverride); err != nil {
			return handlers.ProjectOverridesView{}, fmt.Errorf("write repo override: %w", err)
		}
	}
	if path := config.UserOverridePath(projectID); path != "" {
		if err := config.WriteOverride(path, userOverride); err != nil {
			return handlers.ProjectOverridesView{}, fmt.Errorf("write user override: %w", err)
		}
	}
	if snap.srv != nil {
		snap.srv.InvalidateProjectConfig(projectID)
	}
	return a.GetProjectOverrides(ctx, projectID)
}

// projectGitRoot finds the local git root for projectID. Consults the
// project_locations registry first (populated whenever a client call
// arrives with a cwd) and only falls back to the server's own cwd as a
// last resort.
//
// Bug history: the prior version used a.gitRoot() unconditionally,
// which returned the directory yullu was launched from. Reading or
// writing project B's repo-layer overrides while the server ran out of
// project A's directory silently corrupted project A's
// .yullu/config.toml. Same root cause as the writer-per-project fix,
// missed on the override path.
func (a *App) projectGitRoot(ctx context.Context, projectID string, st *store.Store) string {
	if st != nil && projectID != "" {
		if root, err := st.ProjectGitRoot(ctx, projectID); err == nil && root != "" {
			return root
		}
	}
	return a.gitRoot()
}

// gitRoot returns the working tree the desktop server was launched from.
// This is currently a single-CWD limitation - all repo-layer overrides
// live under this one git root, regardless of which project the UI is
// inspecting. A future per-project clone-aware design can lift this.
func (a *App) gitRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return scope.GitRoot(cwd)
}

// readOverride is a small wrapper that swallows the not-exist error - a
// missing override file is the normal case for projects that haven't been
// customised. Real read errors (permissions, malformed TOML) propagate via
// the warnings slice rather than blowing up the GET response.
func readOverride(path string, allowSecrets bool) (config.ConfigOverride, []string) {
	if path == "" {
		return config.ConfigOverride{}, nil
	}
	override, warnings, err := config.LoadOverride(path, allowSecrets)
	if err != nil {
		return config.ConfigOverride{}, append(warnings, err.Error())
	}
	return override, warnings
}

// configOverrideToPayload flattens the nested config.ConfigOverride into
// the flat JSON-friendly handlers.ProjectOverridePayload the UI consumes.
func configOverrideToPayload(o config.ConfigOverride) handlers.ProjectOverridePayload {
	var p handlers.ProjectOverridePayload
	if o.Reasoning != nil {
		p.ReasoningProvider = o.Reasoning.Provider
		p.ReasoningModel = o.Reasoning.Model
	}
	if o.Voyage != nil {
		p.VoyageAPIKey = o.Voyage.APIKey
	}
	if o.OpenAI != nil {
		p.OpenAIAPIKey = o.OpenAI.APIKey
	}
	if o.Anthropic != nil {
		p.AnthropicAPIKey = o.Anthropic.APIKey
	}
	if o.Sync != nil {
		p.SyncEnabled = o.Sync.Enabled
		p.SyncDir = o.Sync.Dir
		p.SyncLogEmbeddings = o.Sync.LogEmbeddings
		p.SyncReuseEmbeddings = o.Sync.ReuseEmbeddings
		p.SyncAutoReconcileOnStartup = o.Sync.AutoReconcileOnStartup
	}
	if o.Dreaming != nil {
		p.DreamingEnabled = o.Dreaming.Enabled
		p.DreamingInterval = o.Dreaming.Interval
		p.DreamingMinMessages = o.Dreaming.MinMessages
		p.DreamingContextMemories = o.Dreaming.ContextMemories
		p.DreamingOnIdleSeconds = o.Dreaming.OnIdleSeconds
	}
	if o.Retrieval != nil {
		p.RetrievalMinSimilarity = o.Retrieval.MinSimilarity
	}
	return p
}

// payloadToConfigOverride is the reverse - lifts the flat JSON payload
// back into the nested config.ConfigOverride structure for persistence.
// Only non-nil fields produce a non-nil sub-section, so the TOML output
// stays minimal.
func payloadToConfigOverride(p handlers.ProjectOverridePayload) config.ConfigOverride {
	var o config.ConfigOverride
	if p.ReasoningProvider != nil || p.ReasoningModel != nil {
		o.Reasoning = &config.ProviderOverride{
			Provider: p.ReasoningProvider,
			Model:    p.ReasoningModel,
		}
	}
	if p.VoyageAPIKey != nil {
		o.Voyage = &config.KeyOverride{APIKey: p.VoyageAPIKey}
	}
	if p.OpenAIAPIKey != nil {
		o.OpenAI = &config.KeyOverride{APIKey: p.OpenAIAPIKey}
	}
	if p.AnthropicAPIKey != nil {
		o.Anthropic = &config.KeyOverride{APIKey: p.AnthropicAPIKey}
	}
	if p.SyncEnabled != nil || p.SyncDir != nil || p.SyncLogEmbeddings != nil ||
		p.SyncReuseEmbeddings != nil || p.SyncAutoReconcileOnStartup != nil {
		o.Sync = &config.SyncOverride{
			Enabled:                p.SyncEnabled,
			Dir:                    p.SyncDir,
			LogEmbeddings:          p.SyncLogEmbeddings,
			ReuseEmbeddings:        p.SyncReuseEmbeddings,
			AutoReconcileOnStartup: p.SyncAutoReconcileOnStartup,
		}
	}
	if p.DreamingEnabled != nil || p.DreamingInterval != nil ||
		p.DreamingMinMessages != nil || p.DreamingContextMemories != nil ||
		p.DreamingOnIdleSeconds != nil {
		o.Dreaming = &config.DreamingOverride{
			Enabled:         p.DreamingEnabled,
			Interval:        p.DreamingInterval,
			MinMessages:     p.DreamingMinMessages,
			ContextMemories: p.DreamingContextMemories,
			OnIdleSeconds:   p.DreamingOnIdleSeconds,
		}
	}
	if p.RetrievalMinSimilarity != nil {
		o.Retrieval = &config.RetrievalOverride{MinSimilarity: p.RetrievalMinSimilarity}
	}
	return o
}

// effectiveFromConfig flattens the resolved Config into the UI-friendly
// shape. Mirrors the existing ConfigView used by the global Settings tab
// but lives in handlers/ as a separate type to keep the boundaries clean.
func effectiveFromConfig(c config.Config) handlers.EffectiveProjectConfig {
	return handlers.EffectiveProjectConfig{
		EmbeddingProvider:       c.Embedding.Provider,
		EmbeddingModel:          c.Embedding.Model,
		ReasoningProvider:       c.Reasoning.Provider,
		ReasoningModel:          c.Reasoning.Model,
		VoyageAPIKey:            c.Voyage.APIKey,
		OpenAIAPIKey:            c.OpenAI.APIKey,
		AnthropicAPIKey:         c.Anthropic.APIKey,
		SyncEnabled:             c.Sync.Enabled,
		SyncDir:                 c.Sync.Dir,
		DreamingEnabled:         c.Dreaming.Enabled,
		DreamingInterval:        c.Dreaming.Interval,
		DreamingMinMessages:     c.Dreaming.MinMessages,
		DreamingContextMemories: c.Dreaming.ContextMemories,
		DreamingOnIdleSeconds:   c.Dreaming.OnIdleSeconds,
		RetrievalMinSimilarity:  c.Retrieval.MinSimilarity,
	}
}
