// Package server wires the MCP tool surface onto the store + embedder.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/tomanagle/yullu/internal/ai"
	"github.com/tomanagle/yullu/internal/config"
	"github.com/tomanagle/yullu/internal/memlog"
	"github.com/tomanagle/yullu/internal/scope"
	"github.com/tomanagle/yullu/internal/store"
)

// Server holds the MCP server and its dependencies. cfg is the global
// base config; per-project resolution stacks .yullu/config.toml and the
// user-private file on top of it via resolveProject().
type Server struct {
	mcp      *mcpsrv.MCPServer
	store    *store.Store
	embedder ai.Embedder
	reasoner ai.Reasoner
	cfg      config.Config
	logger   *slog.Logger
	// writer is the default event-log writer, built from the global
	// SyncConfig at construction. Per-project sync_dir overrides will
	// switch this to writerForProject() at the call sites that need it;
	// see TODO marker in the dream/reconcile paths.
	writer *memlog.Writer
	// dreamMu serialises dream passes - concurrent dreams against the same
	// session would race on the "read messages then delete" step.
	dreamMu sync.Mutex
	// dreamStateMu guards lastMessageRecordedAt, which feeds the idle
	// trigger of the dream scheduler.
	dreamStateMu          sync.Mutex
	lastMessageRecordedAt time.Time
	// dreamProgressMu guards dreamProgress, the live-progress snapshot
	// the UI polls via /api/dream/progress. Held briefly per mutation —
	// never across reasoner calls — so the HTTP handler never stalls.
	dreamProgressMu sync.Mutex
	dreamProgress   DreamProgress
	// lastScheduledDreamAt is when the scheduler last *fired* a pass for
	// each project (not manual / dream_now calls). Used by
	// DreamProgressSnapshot to compute the next-interval trigger per
	// project. Tracked per-project because the scheduler polls every
	// project with pending messages and decides independently — a busy
	// project shouldn't reset the interval clock for a quiet one.
	lastScheduledDreamAtMu sync.Mutex
	lastScheduledDreamAt   map[string]time.Time
	// projectCfgMu guards the resolved-config cache.
	projectCfgMu sync.RWMutex
	projectCfg   map[string]config.Config
	// schedulerCancel cancels the background dream scheduler started by
	// StartScheduler. Held so a SaveConfig that builds a new Server can
	// stop the previous instance's scheduler before launching a new
	// one — without this, every SaveConfig leaks a goroutine that keeps
	// polling and competing for the same store. Nil before the first
	// StartScheduler call and after StopScheduler.
	schedulerMu     sync.Mutex
	schedulerCancel context.CancelFunc
}

// New constructs a Server with all tools registered. cfg is the global
// base config; per-project resolution happens at call time. logger must
// be non-nil - use applog.Discard() in tests.
func New(s *store.Store, e ai.Embedder, r ai.Reasoner, cfg config.Config, logger *slog.Logger) *Server {
	srv := &Server{
		mcp: mcpsrv.NewMCPServer(
			"yullu",
			"0.1.0",
			mcpsrv.WithToolCapabilities(true),
		),
		store:                s,
		embedder:             e,
		reasoner:             r,
		cfg:                  cfg,
		logger:               logger.With("component", "server"),
		projectCfg:           make(map[string]config.Config),
		lastScheduledDreamAt: make(map[string]time.Time),
	}
	// Advertise sampling so clients (Claude Code, Codex, Cursor) know we may
	// ask them to run LLM completions on our behalf. The dreamer uses this
	// for foreground passes so users can leverage their Pro/Plus subscription
	// instead of paying for a separate API key.
	srv.mcp.EnableSampling()
	if cfg.Sync.Enabled {
		cwd, err := os.Getwd()
		if err == nil {
			srv.writer = memlog.NewWriter(scope.GitRoot(cwd), cfg.Sync.Dir)
		}
		if srv.writer == nil {
			srv.logger.Warn("sync enabled but no git repo found; memory log will not be written", "cwd", cwd)
		} else {
			srv.logger.Info("memory log initialised", "dir", srv.writer.Dir())
		}
	}
	srv.registerTools()
	return srv
}

// resolveProject returns the effective config for a project: global base
// + repo-committed overrides + user-private overrides. Cached per
// projectID; InvalidateProjectConfig drops the entry when overrides
// change.
func (s *Server) resolveProject(projectID string) config.Config {
	if projectID == "" {
		return s.cfg
	}
	s.projectCfgMu.RLock()
	if c, ok := s.projectCfg[projectID]; ok {
		s.projectCfgMu.RUnlock()
		return c
	}
	s.projectCfgMu.RUnlock()

	gitRoot := ""
	if cwd, err := os.Getwd(); err == nil {
		gitRoot = scope.GitRoot(cwd)
	}
	effective, warnings, err := config.Resolve(s.cfg, gitRoot, projectID)
	if err != nil {
		// Override resolution errors are non-fatal - fall back to global.
		s.logger.Warn("resolve project config", "project_id", projectID, "err", err.Error())
		effective = s.cfg
	}
	for _, w := range warnings {
		s.logger.Warn("project override warning", "project_id", projectID, "msg", w)
	}

	s.projectCfgMu.Lock()
	s.projectCfg[projectID] = effective
	s.projectCfgMu.Unlock()
	return effective
}

// InvalidateProjectConfig drops the cached resolved config for projectID
// so the next resolveProject() picks up freshly-edited override files.
// Called by app.SaveProjectOverrides after a write succeeds.
func (s *Server) InvalidateProjectConfig(projectID string) {
	s.projectCfgMu.Lock()
	delete(s.projectCfg, projectID)
	s.projectCfgMu.Unlock()
}

// ServeStdio runs the server over stdio until ctx is cancelled or stdin closes.
func (s *Server) ServeStdio(ctx context.Context) error {
	return mcpsrv.ServeStdio(s.mcp, mcpsrv.WithStdioContextFunc(func(_ context.Context) context.Context {
		return ctx
	}))
}

// ServeHTTP runs the server standalone over Streamable HTTP at the given addr
// (e.g. ":8080"). The MCP endpoint is /mcp. Blocks until ctx is cancelled,
// then shuts down gracefully. Used by the CLI binary's HTTP transport mode;
// the desktop app embeds MCPHandler() in its own mux instead.
func (s *Server) ServeHTTP(ctx context.Context, addr string) error {
	httpSrv := mcpsrv.NewStreamableHTTPServer(s.mcp)

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Start(addr) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// MCPHandler returns the streamable-HTTP MCP handler so callers can mount
// it on their own mux alongside other routes (e.g. the desktop's REST API
// and static frontend).
func (s *Server) MCPHandler() http.Handler {
	return mcpsrv.NewStreamableHTTPServer(s.mcp)
}

func (s *Server) registerTools() {
	s.mcp.AddTool(mcp.NewTool("store_memory",
		mcp.WithDescription("Save a memory so future sessions in this codebase can recall it. Scoped automatically to the current git repo unless project_id is provided. ALWAYS classify with one of the five categories so the agent can retrieve only the kinds of facts relevant to its task."),
		mcp.WithString("content", mcp.Required(), mcp.Description("The memory text to store. Be specific and self-contained - include the why, not just the what, in forward-looking voice.")),
		mcp.WithString("category", mcp.Description("One of: 'process' (how to do things - commands, conventions, layout), 'decision' (why X was chosen over Y), 'gotcha' (non-obvious constraints, what bites), 'domain' (what words mean here, invariants), 'style' (UI patterns, copy tone, visual language). Required for useful retrieval — omit only when the category is genuinely unclear and you want the user to triage it.")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Optional free-form tags for filtering/listing.")),
		mcp.WithString("project_id", mcp.Description("Override project scope. Defaults to the git root containing the caller's cwd (or the server's cwd if neither cwd nor project_id is set).")),
		mcp.WithString("cwd", mcp.Description("Caller's working directory. Without this the server falls back to its own cwd — almost never what you want.")),
	), s.handleStore)

	s.mcp.AddTool(mcp.NewTool("retrieve_memories",
		mcp.WithDescription("Semantically search memories for the current project. Returns the top matches by embedding similarity, optionally filtered to one or more categories. Filter to the categories that match the task you're about to do — process/style for writing or modifying code, decision/gotcha for changing existing behaviour, domain when you have to ask what a term means."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language query - the question or topic you want context on.")),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default 5).")),
		mcp.WithArray("categories", mcp.WithStringItems(), mcp.Description("Optional filter. One or more of: 'process' (how to do things), 'decision' (why X was chosen), 'gotcha' (what bites), 'domain' (what terms mean), 'style' (UI patterns + tone). Omit for no filter (every category). Unknown values are dropped silently.")),
		mcp.WithString("project_id", mcp.Description("Override project scope.")),
		mcp.WithString("cwd", mcp.Description("Caller's working directory; used to resolve the project when project_id isn't set.")),
	), s.handleRetrieve)

	s.mcp.AddTool(mcp.NewTool("update_memory",
		mcp.WithDescription("Update a stored memory's content and/or tags. Re-embeds if content changes."),
		mcp.WithNumber("id", mcp.Required(), mcp.Description("Memory ID to update.")),
		mcp.WithString("content", mcp.Description("New content. Omit to leave unchanged.")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("New tags. Omit to leave unchanged.")),
	), s.handleUpdate)

	s.mcp.AddTool(mcp.NewTool("delete_memory",
		mcp.WithDescription("Permanently delete a memory by ID."),
		mcp.WithNumber("id", mcp.Required(), mcp.Description("Memory ID to delete.")),
	), s.handleDelete)

	s.mcp.AddTool(mcp.NewTool("list_memories",
		mcp.WithDescription("List the most recently updated memories for the current project. Useful when you want an overview rather than a search."),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default 20).")),
		mcp.WithString("project_id", mcp.Description("Override project scope.")),
		mcp.WithString("cwd", mcp.Description("Caller's working directory; used to resolve the project when project_id isn't set.")),
	), s.handleList)

	s.mcp.AddTool(mcp.NewTool("dream_now",
		mcp.WithDescription("Trigger a dream pass immediately. yullu asks its reasoner to review recorded session messages and extract durable memories (remember/revise/forget operations are applied via the normal write paths, so dreamed changes show up in .yullu/logs/). Defaults to dreaming every session for the current project; pass session_id to limit to one."),
		mcp.WithString("session_id", mcp.Description("Optional session_id to dream just that one session.")),
		mcp.WithString("project_id", mcp.Description("Override project scope. Defaults to the git remote.")),
		mcp.WithString("cwd", mcp.Description("Caller's working directory; used to resolve the project when project_id isn't set.")),
	), s.handleDreamNow)

	s.mcp.AddTool(mcp.NewTool("record_messages",
		mcp.WithDescription("Record conversation turns into the dream buffer. Call after each user-assistant exchange so yullu can later 'dream' over them and extract durable memories (decisions, gotchas, project facts) without the human having to flag them explicitly. The raw messages are stored locally only - they are never published to .yullu/logs/."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("A stable string identifying this chat conversation. Use the same value across turns within one chat (any unique-per-chat string works - a UUID, a timestamp, etc.).")),
		mcp.WithArray("messages", mcp.Required(), mcp.Description("Array of objects with keys 'role' ('user' or 'assistant') and 'content' (the message text).")),
		mcp.WithString("project_id", mcp.Description("Override project scope. Defaults to the git remote of the caller's cwd (or the server's cwd if neither cwd nor project_id is set).")),
		mcp.WithString("cwd", mcp.Description("Caller's working directory. The server uses this to resolve the project — without it, the project defaults to whatever directory yullu was launched from, which is almost never what you want.")),
	), s.handleRecordMessages)

	s.mcp.AddTool(mcp.NewTool("reconcile_memories",
		mcp.WithDescription("Sync local memories with the .yullu/logs/ event log for the caller's project. Pulls events committed by teammates, applies them locally, and publishes any local-only memories so others can see them. Safe to run repeatedly. Pass cwd (and/or project_id) to scope to your project — otherwise reconciles only the directory yullu was launched from."),
		mcp.WithString("project_id", mcp.Description("Override project scope.")),
		mcp.WithString("cwd", mcp.Description("Caller's working directory; resolves the project when project_id isn't set.")),
	), s.handleReconcile)

	s.mcp.AddTool(mcp.NewTool("get_usage",
		mcp.WithDescription("Report aggregate model usage: calls, tokens, cost, latency by provider+model+kind. Cost is reported in USD microcents (10⁻⁶ cent) as int64 to avoid float precision drift on aggregation; divide by 10⁶ for cents or 10⁸ for dollars."),
		mcp.WithNumber("since_hours", mcp.Description("Only include events from the last N hours. Omit for all-time totals.")),
		mcp.WithBoolean("recent", mcp.Description("If true, also return the most recent raw events (newest first).")),
		mcp.WithNumber("recent_limit", mcp.Description("How many raw events to return when recent=true (default 20).")),
	), s.handleUsage)
}

// resolveProjectID picks the project for an incoming call. Priority:
//
//  1. explicit override (caller passed project_id)
//  2. caller cwd (the dir the user is actually working in — the Stop
//     hook forwards Claude Code's cwd, MCP clients can pass it too)
//  3. server's own cwd (last resort; this is the dir yullu was launched
//     from, so it's almost always wrong for hook-driven calls — leaving
//     it as a fallback only so direct `yullu` CLI invocations still work)
//
// Bug history: priority 3 used to be the ONLY behaviour, which meant
// every Stop-hook POST resolved to yullu's own repo regardless of where
// the user was working.
func (s *Server) resolveProjectID(override, callerCwd string) (string, error) {
	if override != "" {
		// Override skips cwd → we still try to record a mapping if cwd
		// happened to come along too. Helps the next call from this
		// project that doesn't pass project_id (e.g. dream pass).
		s.maybeRememberProjectLocation(override, callerCwd)
		return override, nil
	}
	if callerCwd != "" {
		projectID, err := scope.Resolve(callerCwd)
		if err != nil {
			return "", err
		}
		s.maybeRememberProjectLocation(projectID, callerCwd)
		return projectID, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	projectID, err := scope.Resolve(cwd)
	if err != nil {
		return "", err
	}
	s.maybeRememberProjectLocation(projectID, cwd)
	return projectID, nil
}

// maybeRememberProjectLocation upserts the (projectID, git_root) pair
// into the store so the dreamer + reconcile can write to the project's
// own .yullu/logs/ instead of the server's cwd. Best-effort: errors
// are logged but don't fail the request. Empty inputs are no-ops.
func (s *Server) maybeRememberProjectLocation(projectID, cwd string) {
	if projectID == "" || cwd == "" {
		return
	}
	gitRoot := scope.GitRoot(cwd)
	if gitRoot == "" {
		return
	}
	if err := s.store.UpsertProjectLocation(context.Background(), projectID, gitRoot); err != nil {
		s.logger.Warn("upsert project location", "project_id", projectID, "git_root", gitRoot, "err", err.Error())
	}
}

// logEvent writes e to the event log if logging is enabled. Project-
// agnostic — used by paths that don't know which project the event
// belongs to. Prefer logEventFor when you have a project_id; it ensures
// the .yullu/logs/ entry lands in the project's own repo instead of the
// server's cwd.
func (s *Server) logEvent(e memlog.Event) error {
	if s.writer == nil {
		return nil
	}
	return s.writer.Write(e)
}

// logEventFor writes e to the .yullu/logs/ directory of the project
// it belongs to. Looks up the project's git root in the registry; if
// found, uses a project-scoped writer. If not found (project hasn't
// been seen with a cwd yet), falls back to the boot-time writer so the
// legacy single-project case keeps working.
//
// Why this exists: the boot-time s.writer is bound to whatever cwd
// yullu was launched from. Without per-project lookup, every project's
// memory events land in the yullu server's repo instead of the
// project's own repo — wrong place for sync, terrible for teammates.
func (s *Server) logEventFor(ctx context.Context, projectID string, e memlog.Event) error {
	w := s.writerForProject(ctx, projectID)
	if w == nil {
		return nil
	}
	return w.Write(e)
}

// writerForProject returns a memlog.Writer scoped to projectID's local
// git root, or the server's boot-time writer as a fallback. Returns nil
// when sync is globally disabled and there's no fallback either.
func (s *Server) writerForProject(ctx context.Context, projectID string) *memlog.Writer {
	if projectID == "" {
		return s.writer
	}
	// Per-project sync config — a project that disabled sync overrides
	// the global default. If sync is off for this project we don't
	// write anywhere.
	syncCfg := s.resolveProject(projectID).Sync
	if !syncCfg.Enabled {
		return nil
	}
	root, err := s.store.ProjectGitRoot(ctx, projectID)
	if err != nil {
		s.logger.Warn("lookup project git root", "project_id", projectID, "err", err.Error())
		return s.writer
	}
	if root == "" {
		// Unknown project — fall back so single-project setups still
		// work. A multi-project user will trigger the registry as soon
		// as any cwd-bearing call arrives.
		return s.writer
	}
	return memlog.NewWriter(root, syncCfg.Dir)
}

// ownVectors returns a single-entry vectors map for our embedder ID, or
// nil if vec is nil or LogEmbeddings is disabled for the given project.
// Per-project gating: a project that opts out of vector publishing
// via [sync].log_embeddings = false in its override file must not leak
// embeddings into the shared event log, even when global sync logs them.
func (s *Server) ownVectors(projectID string, vec []float32) map[string][]float32 {
	if vec == nil {
		return nil
	}
	if !s.resolveProject(projectID).Sync.LogEmbeddings {
		return nil
	}
	return map[string][]float32{s.embedder.ID(): vec}
}

func (s *Server) handleStore(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tags := req.GetStringSlice("tags", nil)
	projectOverride := req.GetString("project_id", "")
	callerCwd := req.GetString("cwd", "")
	// Category is optional via MCP — clients that don't classify (older
	// integrations, terse calls) get an empty value and the memory shows
	// up uncategorised in the Review queue.
	category := store.MemoryCategory(req.GetString("category", ""))
	projectID, err := s.resolveProjectID(projectOverride, callerCwd)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}
	memUUID, id, err := s.createMemory(ctx, projectID, content, tags, category)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("create", err), nil
	}
	return jsonResult(map[string]any{
		"id":         id,
		"uuid":       memUUID,
		"project_id": projectID,
	})
}

// createMemory is the shared write path used by store_memory and the dreamer.
// Embeds content, writes the create event (with our vector if log_embeddings),
// then inserts the row. Returns the new memory's UUID and local ID.
// category may be empty (memory will be uncategorised; user can fix in
// the Review queue) or one of the canonical MemoryCategory values.
func (s *Server) createMemory(ctx context.Context, projectID, content string, tags []string, category store.MemoryCategory) (string, int64, error) {
	vecs, err := s.embedder.Embed(ctx, []string{content})
	if err != nil {
		return "", 0, fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != 1 {
		return "", 0, fmt.Errorf("embedder returned %d vectors, expected 1", len(vecs))
	}
	memUUID := uuid.NewString()
	event := memlog.NewRememberEvent(memUUID, content, tags, s.ownVectors(projectID, vecs[0]))
	if err := s.logEventFor(ctx, projectID, event); err != nil {
		return "", 0, fmt.Errorf("log create event: %w", err)
	}
	id, err := s.store.Insert(ctx, memUUID, projectID, content, tags, vecs[0], category)
	if err != nil {
		return "", 0, fmt.Errorf("store insert: %w", err)
	}
	return memUUID, id, nil
}

// updateMemoryByUUID embeds new content when provided, writes the update
// event, and patches the local row. Returns the patched memory.
func (s *Server) updateMemoryByUUID(ctx context.Context, memUUID string, contentPtr *string, tagsPtr *[]string) (*store.Memory, error) {
	existing, err := s.store.GetByUUID(ctx, memUUID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("memory %s not found", memUUID)
	}
	var newVec []float32
	if contentPtr != nil {
		vecs, err := s.embedder.Embed(ctx, []string{*contentPtr})
		if err != nil {
			return nil, fmt.Errorf("embed: %w", err)
		}
		newVec = vecs[0]
	}
	event := memlog.NewReviseEvent(memUUID, contentPtr, tagsPtr, s.ownVectors(existing.ProjectID, newVec))
	if err := s.logEventFor(ctx, existing.ProjectID, event); err != nil {
		return nil, fmt.Errorf("log update event: %w", err)
	}
	if err := s.store.Update(ctx, existing.ID, contentPtr, tagsPtr, newVec); err != nil {
		return nil, fmt.Errorf("store update: %w", err)
	}
	return s.store.GetByUUID(ctx, memUUID)
}

// deleteMemoryByUUID writes the delete event and removes the local row.
func (s *Server) deleteMemoryByUUID(ctx context.Context, memUUID string) error {
	existing, err := s.store.GetByUUID(ctx, memUUID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("memory %s not found", memUUID)
	}
	if err := s.logEventFor(ctx, existing.ProjectID, memlog.NewForgetEvent(memUUID)); err != nil {
		return fmt.Errorf("log delete event: %w", err)
	}
	return s.store.Delete(ctx, existing.ID)
}

func (s *Server) handleRetrieve(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", 5)
	projectOverride := req.GetString("project_id", "")
	callerCwd := req.GetString("cwd", "")
	// Category filter — Search validates + de-dupes, so we just lift the
	// raw strings into the typed slice and pass through.
	rawCats := req.GetStringSlice("categories", nil)
	cats := make([]store.MemoryCategory, 0, len(rawCats))
	for _, c := range rawCats {
		cats = append(cats, store.MemoryCategory(c))
	}
	projectID, err := s.resolveProjectID(projectOverride, callerCwd)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}

	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("embed", err), nil
	}
	hits, err := s.store.Search(ctx, projectID, vecs[0], limit, cats)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("search", err), nil
	}
	return jsonResult(map[string]any{
		"project_id": projectID,
		"results":    hits,
	})
}

func (s *Server) handleUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireInt("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	args := req.GetArguments()

	var (
		contentPtr *string
		tagsPtr    *[]string
	)
	if raw, ok := args["content"]; ok {
		if str, ok := raw.(string); ok {
			contentPtr = &str
		}
	}
	if raw, ok := args["tags"]; ok {
		if arr, ok := raw.([]any); ok {
			tags := make([]string, 0, len(arr))
			for _, v := range arr {
				if str, ok := v.(string); ok {
					tags = append(tags, str)
				}
			}
			tagsPtr = &tags
		}
	}
	if contentPtr == nil && tagsPtr == nil {
		return mcp.NewToolResultError("nothing to update: provide content and/or tags"), nil
	}

	// LLM gives us the local int ID; look up the UUID before going through
	// the shared update path.
	existing, err := s.store.Get(ctx, int64(id))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get", err), nil
	}
	m, err := s.updateMemoryByUUID(ctx, existing.UUID, contentPtr, tagsPtr)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("update", err), nil
	}
	return jsonResult(m)
}

func (s *Server) handleDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireInt("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	existing, err := s.store.Get(ctx, int64(id))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get", err), nil
	}
	if err := s.deleteMemoryByUUID(ctx, existing.UUID); err != nil {
		return mcp.NewToolResultErrorFromErr("delete", err), nil
	}
	return jsonResult(map[string]any{"id": id, "uuid": existing.UUID, "deleted": true})
}

func (s *Server) handleDreamNow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// dream_now bypasses min_messages - explicit manual triggers should
	// process whatever's in the buffer, even small sessions.
	//
	// Per-project resolution: a project that overrides
	// [dreaming].context_memories must see its own value, not the
	// global default. Bug history: the prior version read
	// s.cfg.Dreaming.ContextMemories unconditionally, so MCP dream_now
	// ignored overrides that every other dream-firing path honoured.
	projectOverride := req.GetString("project_id", "")
	callerCwd := req.GetString("cwd", "")
	projectID, err := s.resolveProjectID(projectOverride, callerCwd)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}
	dc := s.resolveProject(projectID).Dreaming
	res, err := s.Dream(ctx, DreamOptions{
		ProjectID:       projectID,
		SessionFilter:   req.GetString("session_id", ""),
		ContextMemories: dc.ContextMemories,
	})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("dream", err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleRecordMessages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if sessionID == "" {
		return mcp.NewToolResultError("session_id must be non-empty"), nil
	}

	args := req.GetArguments()
	rawMsgs, ok := args["messages"]
	if !ok {
		return mcp.NewToolResultError("messages is required"), nil
	}
	arr, ok := rawMsgs.([]any)
	if !ok {
		return mcp.NewToolResultError("messages must be an array"), nil
	}
	msgs := make([]store.SessionMessageInput, 0, len(arr))
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("messages[%d] must be an object", i)), nil
		}
		role, _ := obj["role"].(string)
		content, _ := obj["content"].(string)
		if role != "user" && role != "assistant" {
			return mcp.NewToolResultError(fmt.Sprintf("messages[%d].role must be 'user' or 'assistant', got %q", i, role)), nil
		}
		if content == "" {
			return mcp.NewToolResultError(fmt.Sprintf("messages[%d].content must be non-empty", i)), nil
		}
		msgs = append(msgs, store.SessionMessageInput{Role: role, Content: content})
	}

	projectOverride := req.GetString("project_id", "")
	callerCwd := req.GetString("cwd", "")
	projectID, err := s.resolveProjectID(projectOverride, callerCwd)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}

	if err := s.store.RecordSessionMessages(ctx, projectID, sessionID, msgs); err != nil {
		return mcp.NewToolResultErrorFromErr("record", err), nil
	}
	s.markMessageRecorded()
	return jsonResult(map[string]any{
		"session_id": sessionID,
		"project_id": projectID,
		"count":      len(msgs),
	})
}

// RecordSessionMessages is the non-MCP path into the session_messages
// buffer. Used by the REST endpoint POST /api/messages (and therefore by
// `yullu record-turn` from the Claude Code Stop hook). projectOverride
// and callerCwd may both be empty; resolveProjectID walks the priority
// chain (override → callerCwd → server cwd) to land on a project. The
// Stop hook forwards Claude Code's cwd here so we don't end up scoping
// every recorded turn to whatever directory yullu itself was launched
// from.
func (s *Server) RecordSessionMessages(
	ctx context.Context,
	projectOverride, callerCwd, sessionID string,
	msgs []store.SessionMessageInput,
) (string, error) {
	projectID, err := s.resolveProjectID(projectOverride, callerCwd)
	if err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}
	if err := s.store.RecordSessionMessages(ctx, projectID, sessionID, msgs); err != nil {
		return projectID, err
	}
	s.markMessageRecorded()
	return projectID, nil
}

// markMessageRecorded stamps the wall-clock time of the most recent
// record_messages call. The dream scheduler reads this to decide whether
// the idle trigger has fired.
func (s *Server) markMessageRecorded() {
	s.dreamStateMu.Lock()
	s.lastMessageRecordedAt = time.Now()
	s.dreamStateMu.Unlock()
}

func (s *Server) lastMessageTime() time.Time {
	s.dreamStateMu.Lock()
	defer s.dreamStateMu.Unlock()
	return s.lastMessageRecordedAt
}

func (s *Server) handleReconcile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Forward project_id / cwd so reconcile lands on the caller's
	// project, not the yullu server's cwd.
	res, err := s.Reconcile(ctx, req.GetString("project_id", ""), req.GetString("cwd", ""))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("reconcile", err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := req.GetInt("limit", 20)
	projectOverride := req.GetString("project_id", "")
	callerCwd := req.GetString("cwd", "")
	projectID, err := s.resolveProjectID(projectOverride, callerCwd)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}
	out, err := s.store.List(ctx, projectID, limit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list", err), nil
	}
	return jsonResult(map[string]any{
		"project_id": projectID,
		"memories":   out,
	})
}

func (s *Server) handleUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	since := time.Time{}
	if h := req.GetFloat("since_hours", 0); h > 0 {
		since = time.Now().Add(-time.Duration(h * float64(time.Hour)))
	}
	buckets, err := s.store.UsageSummary(ctx, since)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("usage summary", err), nil
	}

	var totalMicrocents int64
	totalCalls := 0
	for _, b := range buckets {
		totalMicrocents += b.CostMicrocentsUSD
		totalCalls += b.Calls
	}

	out := map[string]any{
		"buckets":                   buckets,
		"total_calls":               totalCalls,
		"total_cost_microcents_usd": totalMicrocents,
	}
	if req.GetBool("recent", false) {
		limit := req.GetInt("recent_limit", 20)
		recent, err := s.store.UsageRecent(ctx, limit)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("recent usage", err), nil
		}
		out["recent"] = recent
	}
	if !since.IsZero() {
		out["since"] = since
	}
	return jsonResult(out)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}
