package handlers

import "net/http"

// RegisterParams bundles every dependency the REST handlers collectively
// need. main.go constructs this from concrete types (the App's state
// machine + the *store.Store + *server.Server) and hands it to Register,
// which builds each handler and mounts it on the mux.
type RegisterParams struct {
	Status           StatusService
	Config           ConfigService
	Memory           MemoryReader
	Editor           MemoryEditor
	Projects         ProjectLister
	Graph            GraphReader
	Stats            MemoryStatsReader
	Usage            UsageReader
	Dreamer          Dreamer
	Session          SessionStatsProvider
	DreamStats       DreamStatsReader
	ProjectOverrides ProjectOverridesService
	Messages         MessageRecorder

	// DreamContextMemories is the [dreaming].context_memories value the
	// PostDream handler passes to the dreamer on each call. Read once at
	// registration time; SaveConfig rebuilds routes (via Status.Retry → main)
	// if the user changes it.
	DreamContextMemories int
}

// Register mounts every JSON REST endpoint on mux. The shape is one-pattern
// per call so a reader can grep for `GET /api/...` in this file and find
// the route table at a glance.
func Register(mux *http.ServeMux, p RegisterParams) {
	mux.Handle("GET /api/status", NewGetStatusHandler(GetStatusHandlerParams{Status: p.Status}))
	mux.Handle("POST /api/retry", NewPostRetryHandler(PostRetryHandlerParams{Status: p.Status}))

	mux.Handle("GET /api/config", NewGetConfigHandler(GetConfigHandlerParams{Config: p.Config}))
	mux.Handle("POST /api/config", NewPostConfigHandler(PostConfigHandlerParams{Config: p.Config}))

	mux.Handle("GET /api/projects", NewGetProjectsHandler(GetProjectsHandlerParams{Projects: p.Projects}))

	mux.Handle("GET /api/memories", NewGetMemoriesHandler(GetMemoriesHandlerParams{Memory: p.Memory}))
	mux.Handle("PUT /api/memories/{id}", NewPutMemoryHandler(PutMemoryHandlerParams{Editor: p.Editor}))
	mux.Handle("DELETE /api/memories/{id}", NewDeleteMemoryHandler(DeleteMemoryHandlerParams{Editor: p.Editor}))

	mux.Handle("GET /api/sessions/stats", NewGetSessionStatsHandler(GetSessionStatsHandlerParams{Session: p.Session}))

	mux.Handle("POST /api/dream", NewPostDreamHandler(PostDreamHandlerParams{
		Dreamer:         p.Dreamer,
		ContextMemories: p.DreamContextMemories,
	}))
	mux.Handle("GET /api/dream/stats", NewGetDreamStatsHandler(GetDreamStatsHandlerParams{
		Stats: p.DreamStats,
	}))

	mux.Handle("GET /api/stats", NewGetStatsHandler(GetStatsHandlerParams{Stats: p.Stats}))
	mux.Handle("GET /api/stats/events", NewGetStatsEventsHandler(GetStatsEventsHandlerParams{Stats: p.Stats}))

	mux.Handle("GET /api/usage/by-day", NewGetUsageByDayHandler(GetUsageByDayHandlerParams{Usage: p.Usage}))
	mux.Handle("GET /api/usage/summary", NewGetUsageSummaryHandler(GetUsageSummaryHandlerParams{Usage: p.Usage}))

	mux.Handle("GET /api/graph", NewGetGraphHandler(GetGraphHandlerParams{Graph: p.Graph}))

	// Per-project overrides. Path takes the project_id as the trailing
	// segment - url-encoded since project_ids contain slashes
	// ("github.com/owner/repo" → "github.com%2Fowner%2Frepo"). The id with
	// slashes left intact would need a wildcard pattern; we keep things
	// simple with single-segment matching and let the FE encode.
	mux.Handle("GET /api/projects/{id}/overrides",
		NewGetProjectOverridesHandler(GetProjectOverridesHandlerParams{Overrides: p.ProjectOverrides}))
	mux.Handle("POST /api/projects/{id}/overrides",
		NewPostProjectOverridesHandler(PostProjectOverridesHandlerParams{Overrides: p.ProjectOverrides}))

	// Session-message intake for non-MCP clients (Claude Code Stop hook etc.).
	// Mirrors the record_messages MCP tool but speaks plain REST.
	mux.Handle("POST /api/messages",
		NewPostMessagesHandler(PostMessagesHandlerParams{Recorder: p.Messages}))
}
