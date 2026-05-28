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

// Server holds the MCP server and its dependencies.
type Server struct {
	mcp      *mcpsrv.MCPServer
	store    *store.Store
	embedder ai.Embedder
	reasoner ai.Reasoner
	syncCfg  config.SyncConfig
	dreamCfg config.DreamingConfig
	logger   *slog.Logger
	// writer is nil when sync is disabled or the server isn't inside a git
	// repo. Handlers must check before logging.
	writer *memlog.Writer
	// dreamMu serialises dream passes - concurrent dreams against the same
	// session would race on the "read messages then delete" step.
	dreamMu sync.Mutex
	// dreamStateMu guards lastMessageRecordedAt, which feeds the idle
	// trigger of the dream scheduler.
	dreamStateMu          sync.Mutex
	lastMessageRecordedAt time.Time
}

// New constructs a Server with all tools registered. syncCfg controls
// .yullu/ event logging; dreamCfg controls the dreamer (scheduler is
// not started here - call StartScheduler from main). logger must be
// non-nil - use applog.Discard() in tests.
func New(s *store.Store, e ai.Embedder, r ai.Reasoner, syncCfg config.SyncConfig, dreamCfg config.DreamingConfig, logger *slog.Logger) *Server {
	srv := &Server{
		mcp: mcpsrv.NewMCPServer(
			"yullu",
			"0.1.0",
			mcpsrv.WithToolCapabilities(true),
		),
		store:    s,
		embedder: e,
		reasoner: r,
		syncCfg:  syncCfg,
		dreamCfg: dreamCfg,
		logger:   logger.With("component", "server"),
	}
	// Advertise sampling so clients (Claude Code, Codex, Cursor) know we may
	// ask them to run LLM completions on our behalf. The dreamer uses this
	// for foreground passes so users can leverage their Pro/Plus subscription
	// instead of paying for a separate API key.
	srv.mcp.EnableSampling()
	if syncCfg.Enabled {
		cwd, err := os.Getwd()
		if err == nil {
			srv.writer = memlog.NewWriter(scope.GitRoot(cwd), syncCfg.Dir)
		}
		if srv.writer == nil {
			srv.logger.Warn("sync enabled but no git repo found; events will not be logged", "cwd", cwd)
		} else {
			srv.logger.Info("event log initialised", "dir", srv.writer.Dir())
		}
	}
	srv.registerTools()
	return srv
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
		mcp.WithDescription("Save a memory (decision, gotcha, pattern, project fact) so future sessions in this codebase can recall it. Scoped automatically to the current git repo unless project_id is provided."),
		mcp.WithString("content", mcp.Required(), mcp.Description("The memory text to store. Be specific and self-contained - include the why, not just the what.")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Optional tags for filtering/listing.")),
		mcp.WithString("project_id", mcp.Description("Override project scope. Defaults to the git root containing the server's working directory.")),
	), s.handleStore)

	s.mcp.AddTool(mcp.NewTool("retrieve_memories",
		mcp.WithDescription("Semantically search memories for the current project. Returns the top matches by embedding similarity."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language query - the question or topic you want context on.")),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default 5).")),
		mcp.WithString("project_id", mcp.Description("Override project scope.")),
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
	), s.handleList)

	s.mcp.AddTool(mcp.NewTool("dream_now",
		mcp.WithDescription("Trigger a dream pass immediately. yullu asks its reasoner to review recorded session messages and extract durable memories (create/update/delete operations are applied via the normal write paths, so dreamed changes show up in .yullu/events/). Defaults to dreaming every session for the current project; pass session_id to limit to one."),
		mcp.WithString("session_id", mcp.Description("Optional session_id to dream just that one session.")),
		mcp.WithString("project_id", mcp.Description("Override project scope. Defaults to the git remote.")),
	), s.handleDreamNow)

	s.mcp.AddTool(mcp.NewTool("record_messages",
		mcp.WithDescription("Record conversation turns into the dream buffer. Call after each user-assistant exchange so yullu can later 'dream' over them and extract durable memories (decisions, gotchas, project facts) without the human having to flag them explicitly. The raw messages are stored locally only - they are never published to .yullu/events/."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("A stable string identifying this chat conversation. Use the same value across turns within one chat (any unique-per-chat string works - a UUID, a timestamp, etc.).")),
		mcp.WithArray("messages", mcp.Required(), mcp.Description("Array of objects with keys 'role' ('user' or 'assistant') and 'content' (the message text).")),
		mcp.WithString("project_id", mcp.Description("Override project scope. Defaults to the git remote of the server's working directory.")),
	), s.handleRecordMessages)

	s.mcp.AddTool(mcp.NewTool("reconcile_memories",
		mcp.WithDescription("Sync local memories with the .yullu/ event log. Pulls events committed by teammates, applies them locally, and publishes any local-only memories so others can see them. Safe to run repeatedly."),
	), s.handleReconcile)

	s.mcp.AddTool(mcp.NewTool("get_usage",
		mcp.WithDescription("Report aggregate model usage: calls, tokens, cost, latency by provider+model+kind. Cost is reported in USD microcents (10⁻⁶ cent) as int64 to avoid float precision drift on aggregation; divide by 10⁶ for cents or 10⁸ for dollars."),
		mcp.WithNumber("since_hours", mcp.Description("Only include events from the last N hours. Omit for all-time totals.")),
		mcp.WithBoolean("recent", mcp.Description("If true, also return the most recent raw events (newest first).")),
		mcp.WithNumber("recent_limit", mcp.Description("How many raw events to return when recent=true (default 20).")),
	), s.handleUsage)
}

func (s *Server) resolveProjectID(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return scope.Resolve(cwd)
}

// logEvent writes e to the event log if logging is enabled. Logging errors
// surface to the caller - for write paths, "event then apply" means a failed
// log write should abort the operation so the local DB doesn't drift.
func (s *Server) logEvent(e memlog.Event) error {
	if s.writer == nil {
		return nil
	}
	return s.writer.Write(e)
}

// ownVectors returns a single-entry vectors map for our embedder ID, or nil
// if vec is nil or LogEmbeddings is disabled. Used by handlers to attach a
// vector to a create/update event.
func (s *Server) ownVectors(vec []float32) map[string][]float32 {
	if vec == nil || !s.syncCfg.LogEmbeddings {
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
	projectID, err := s.resolveProjectID(projectOverride)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}
	memUUID, id, err := s.createMemory(ctx, projectID, content, tags)
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
func (s *Server) createMemory(ctx context.Context, projectID, content string, tags []string) (string, int64, error) {
	vecs, err := s.embedder.Embed(ctx, []string{content})
	if err != nil {
		return "", 0, fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != 1 {
		return "", 0, fmt.Errorf("embedder returned %d vectors, expected 1", len(vecs))
	}
	memUUID := uuid.NewString()
	event := memlog.NewCreateEvent(memUUID, content, tags, s.ownVectors(vecs[0]))
	if err := s.logEvent(event); err != nil {
		return "", 0, fmt.Errorf("log create event: %w", err)
	}
	id, err := s.store.Insert(ctx, memUUID, projectID, content, tags, vecs[0])
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
	event := memlog.NewUpdateEvent(memUUID, contentPtr, tagsPtr, s.ownVectors(newVec))
	if err := s.logEvent(event); err != nil {
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
	if err := s.logEvent(memlog.NewDeleteEvent(memUUID)); err != nil {
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
	projectID, err := s.resolveProjectID(projectOverride)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve project", err), nil
	}

	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("embed", err), nil
	}
	hits, err := s.store.Search(ctx, projectID, vecs[0], limit)
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
	res, err := s.Dream(ctx, DreamOptions{
		SessionFilter:   req.GetString("session_id", ""),
		ContextMemories: s.dreamCfg.ContextMemories,
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
	projectID, err := s.resolveProjectID(projectOverride)
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

func (s *Server) handleReconcile(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	res, err := s.Reconcile(ctx)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("reconcile", err), nil
	}
	return jsonResult(res)
}

func (s *Server) handleList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := req.GetInt("limit", 20)
	projectOverride := req.GetString("project_id", "")
	projectID, err := s.resolveProjectID(projectOverride)
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
