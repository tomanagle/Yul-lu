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
	SessionBuffer    SessionBufferReader
	DreamPrompt      DreamPromptService
	DreamProgress    DreamProgressService
	DreamPasses      DreamPassLister
	Rater            MemoryRater
	Recall           MemoryRecaller

	// DreamContextMemories is a thunk that returns the current
	// [dreaming].context_memories value, read under the App lock on
	// each invocation. Was a captured `int`; converting to a function
	// means SaveConfig changes take effect immediately without anyone
	// having to rebuild the route table.
	DreamContextMemories func() int
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

	// Review queue: list memories awaiting a user rating + accept ratings.
	// Rating ≤ 5 moves the row to rejected_memories (anti-example signal
	// for future dream passes); ≥ 6 keeps + annotates it.
	mux.Handle("GET /api/memories/unrated",
		NewGetUnratedMemoriesHandler(GetUnratedMemoriesHandlerParams{Rater: p.Rater}))
	mux.Handle("POST /api/memories/{id}/rate",
		NewPostRateMemoryHandler(PostRateMemoryHandlerParams{Rater: p.Rater}))

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

	// Read the buffered sessions for the Dreaming page (live view of
	// what the next dream pass will process).
	mux.Handle("GET /api/sessions",
		NewGetSessionsHandler(GetSessionsHandlerParams{Buffer: p.SessionBuffer}))

	// Dream system prompt — read + write.
	mux.Handle("GET /api/dream/prompt",
		NewGetDreamPromptHandler(GetDreamPromptHandlerParams{Prompt: p.DreamPrompt}))
	mux.Handle("POST /api/dream/prompt",
		NewPostDreamPromptHandler(PostDreamPromptHandlerParams{Prompt: p.DreamPrompt}))

	// Live dream-pass progress. Polled by the dashboard while a pass is
	// running; cheap (in-memory snapshot, no DB hit) so a 1–2 second
	// refetch interval is safe.
	mux.Handle("GET /api/dream/progress",
		NewGetDreamProgressHandler(GetDreamProgressHandlerParams{Progress: p.DreamProgress}))

	// Per-cycle dream history for the Stats page.
	mux.Handle("GET /api/dream/passes",
		NewGetDreamPassesHandler(GetDreamPassesHandlerParams{Lister: p.DreamPasses}))

	// Hook-driven memory recall. The UserPromptSubmit hook (`yullu inject`)
	// POSTs the user's prompt + cwd, gets back the top-K relevant
	// memories scoped to the project. Used to ambiently inject context
	// before the agent processes a prompt — agent doesn't have to call
	// retrieve_memories for the common case.
	mux.Handle("POST /api/memories/recall",
		NewPostRecallHandler(PostRecallHandlerParams{Recall: p.Recall}))
}
