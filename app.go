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
	mcpMu      sync.RWMutex
	mcpHandler http.Handler
}

func NewApp() *App {
	return &App{logger: applog.New().With("component", "desktop")}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfgPath = config.MustDefaultPath()
	a.cfg = config.MustLoad(a.cfgPath)
	a.openStore()
}

func (a *App) shutdown(_ context.Context) {
	if a.store != nil {
		_ = a.store.Close()
	}
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

func (a *App) mcpHandlerHTTP() http.Handler {
	a.mcpMu.RLock()
	defer a.mcpMu.RUnlock()
	return a.mcpHandler
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

	reasoner, _ := ai.BuildReasoner(a.cfg, ai.NopRecorder())
	a.srv = server.New(st, embedder, reasoner, a.cfg, a.logger)
	a.swapMCPHandler()

	if a.cfg.Sync.Enabled && a.cfg.Sync.AutoReconcileOnStartup {
		a.srv.LogReconcile(a.ctx)
	}
}

// ---------- StatusService ----------

func (a *App) Status() handlers.Status {
	s := handlers.Status{
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

// Retry re-runs openStore after the user fixes the underlying cause
// (deletes a stale DB, pastes an API key, creates the data directory).
func (a *App) Retry() handlers.Status {
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	a.embedder = nil
	a.openStore()
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
	return handlers.ConfigView{
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

func (a *App) SaveConfig(v handlers.ConfigView) (handlers.Status, error) {
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
	if a.store != nil {
		_ = a.store.Close()
		a.store = nil
	}
	a.openStore()
	return a.Status(), nil
}

// ---------- MemoryReader ----------

func (a *App) List(ctx context.Context, projectID string, limit int) ([]store.Memory, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if limit <= 0 {
		limit = 100
	}
	return a.store.List(ctx, projectID, limit)
}

func (a *App) SearchText(ctx context.Context, projectID, query string, limit int) ([]store.Memory, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	if limit <= 0 {
		limit = 100
	}
	if strings.TrimSpace(query) == "" {
		return a.store.List(ctx, projectID, limit)
	}
	return a.store.SearchText(ctx, projectID, query, limit)
}

// ---------- MemoryEditor ----------

// UpdateMemory patches a memory's content and/or tags. Re-embeds when
// content actually changes so vector search stays accurate. Always
// overwrites tags with the supplied slice (treat empty as "clear").
//
// Heads up: this writes to the local DB only. Desktop-initiated changes
// don't currently propagate to .yullu/events/.
func (a *App) UpdateMemory(ctx context.Context, id int64, content string, tags []string) (*store.Memory, error) {
	if a.store == nil || a.embedder == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	existing, err := a.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	var contentPtr *string
	var newVec []float32
	if content != existing.Content {
		c := content
		contentPtr = &c
		vecs, err := a.embedder.Embed(ctx, []string{c})
		if err != nil {
			return nil, fmt.Errorf("embed: %w", err)
		}
		newVec = vecs[0]
	}
	tagsPtr := &tags

	if err := a.store.Update(ctx, id, contentPtr, tagsPtr, newVec); err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return a.store.Get(ctx, id)
}

func (a *App) DeleteMemory(ctx context.Context, id int64) error {
	if a.store == nil {
		return fmt.Errorf("store not open")
	}
	return a.store.Delete(ctx, id)
}

// ---------- ProjectLister ----------

func (a *App) ListProjects(ctx context.Context) ([]string, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open - finish setup first")
	}
	return a.store.ListProjects(ctx)
}

// ---------- GraphReader ----------

func (a *App) MemoryGraph(ctx context.Context, projectID string) (store.MemoryGraph, error) {
	if a.store == nil {
		return store.MemoryGraph{}, fmt.Errorf("store not open")
	}
	return a.store.MemoryGraph(ctx, projectID)
}

// ---------- MemoryStatsReader ----------

func (a *App) GetMemoryStats(ctx context.Context, projectID string) (store.MemoryStats, error) {
	if a.store == nil {
		return store.MemoryStats{}, fmt.Errorf("store not open")
	}
	return a.store.GetMemoryStats(ctx, projectID)
}

func (a *App) MemoryEventsByDay(ctx context.Context, projectID string, days int) ([]store.DailyMemoryEvents, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return a.store.MemoryEventsByDay(ctx, projectID, days)
}

// ---------- DreamStatsReader ----------

func (a *App) DreamStats(ctx context.Context, projectID string, days int) (store.DreamStats, error) {
	if a.store == nil {
		return store.DreamStats{}, fmt.Errorf("store not open")
	}
	return a.store.DreamStats(ctx, projectID, days)
}

// ---------- UsageReader ----------

func (a *App) UsageByDay(ctx context.Context, days int) ([]store.DailyUsage, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return a.store.UsageByDay(ctx, days)
}

func (a *App) UsageSummary(ctx context.Context, since time.Time) ([]store.UsageBucket, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	return a.store.UsageSummary(ctx, since)
}

// ---------- SessionStatsProvider ----------

func (a *App) GetSessionStats(ctx context.Context, projectID string) (handlers.SessionStats, error) {
	if a.store == nil {
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
	sessions, messages, err := a.store.CountSessionMessages(ctx, projectID)
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
	if a.srv == nil {
		return nil, fmt.Errorf("desktop not initialised - finish setup first")
	}
	return a.srv.Dream(ctx, opts)
}

// ---------- MessageRecorder ----------

// RecordMessages funnels into the server's session-buffer write path
// (same code the record_messages MCP tool uses), so REST-side hooks
// (e.g. `yullu record-turn` invoked by Claude Code's Stop hook) feed
// the dreamer just like MCP clients do.
func (a *App) RecordMessages(
	ctx context.Context,
	projectOverride, sessionID string,
	msgs []handlers.RecordedMessage,
) (string, error) {
	if a.srv == nil {
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
	return a.srv.RecordSessionMessages(ctx, projectOverride, sessionID, inputs)
}

// ---------- ProjectOverridesService ----------

// GetProjectOverrides reads the repo + user override files for projectID,
// stacks them onto the global config to compute the effective view, and
// returns all three so the UI can render inherited values as placeholders.
func (a *App) GetProjectOverrides(_ context.Context, projectID string) (handlers.ProjectOverridesView, error) {
	repoOverride, repoWarn := readOverride(config.RepoOverridePath(a.gitRoot()), false)
	userOverride, userWarn := readOverride(config.UserOverridePath(projectID), true)

	effective := config.Merge(a.cfg, repoOverride)
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
func (a *App) SaveProjectOverrides(_ context.Context, projectID string, repo, user handlers.ProjectOverridePayload) (handlers.ProjectOverridesView, error) {
	// Repo payload: drop API keys before persistence.
	repo.VoyageAPIKey = nil
	repo.OpenAIAPIKey = nil
	repo.AnthropicAPIKey = nil

	repoOverride := payloadToConfigOverride(repo)
	userOverride := payloadToConfigOverride(user)

	if path := config.RepoOverridePath(a.gitRoot()); path != "" {
		if err := config.WriteOverride(path, repoOverride); err != nil {
			return handlers.ProjectOverridesView{}, fmt.Errorf("write repo override: %w", err)
		}
	}
	if path := config.UserOverridePath(projectID); path != "" {
		if err := config.WriteOverride(path, userOverride); err != nil {
			return handlers.ProjectOverridesView{}, fmt.Errorf("write user override: %w", err)
		}
	}
	if a.srv != nil {
		a.srv.InvalidateProjectConfig(projectID)
	}
	return a.GetProjectOverrides(context.Background(), projectID)
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
	}
}
