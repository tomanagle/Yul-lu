// Package handlers contains the REST HTTP handlers exposed by the yullu
// desktop server. One handler per file; each handler is a struct + ServeHTTP
// method, constructed from a Params struct that names its exact dependencies.
//
// DTO types shared with the main package live here so handlers and the App
// state-machine agree on wire shapes without main importing itself.
package handlers

// Status is what GET /api/status returns. Ready is true once the embedder +
// store have opened successfully; Message + Hint describe the actionable
// failure when Ready is false.
type Status struct {
	Ready      bool   `json:"ready"`
	ConfigPath string `json:"config_path"`
	DBPath     string `json:"db_path"`
	Embedder   string `json:"embedder,omitempty"`
	// Reasoner names the direct provider (anthropic|openai) when one is
	// configured. Empty/omitted means "sampling-only mode" — dreams can
	// only run from inside an MCP client session, not from the background
	// scheduler or the desktop "Dream now" button.
	Reasoner string `json:"reasoner,omitempty"`
	Message  string `json:"message,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// ConfigView is the flat JSON projection of the on-disk config.toml - what
// the Settings UI reads and writes. Kept flat (no nested objects) so it
// round-trips through fetch() without ceremony.
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

	// RetrievalMinSimilarity is the cosine-similarity floor (0–1) a memory
	// must clear to be returned by a vector search. 0 disables the floor.
	RetrievalMinSimilarity float64 `json:"retrieval_min_similarity"`
}

// SessionStats is the dream-buffer summary surfaced on the Dreaming page.
type SessionStats struct {
	ProjectID string `json:"project_id"`
	Sessions  int    `json:"sessions"`
	Messages  int    `json:"messages"`
}
