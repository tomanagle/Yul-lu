package handlers

// ProjectOverridesView is the DTO for GET/POST /api/projects/{id}/overrides.
// The two layers are surfaced separately so the UI can render them in
// different sections ("team-shared (committed)" vs "private (this machine)")
// and know exactly where each value will be persisted.
//
// Each pointer field is nil when the override doesn't set it - that's the
// signal to inherit from the next lower layer. Empty string vs nil matters:
// nil means "use default", empty string means "explicitly set to empty".
type ProjectOverridesView struct {
	ProjectID string                 `json:"project_id"`
	Repo      ProjectOverridePayload `json:"repo"`
	User      ProjectOverridePayload `json:"user"`
	// Effective is the resolved view after applying repo + user on top of
	// the global base. Read-only — useful for the UI to render inherited
	// values as placeholders. Never written back.
	Effective EffectiveProjectConfig `json:"effective"`
	// Warnings flag entries that loaded but were ignored (e.g. api_keys
	// stripped from the repo file because committed files must not carry
	// secrets).
	Warnings []string `json:"warnings,omitempty"`
}

// ProjectOverridePayload is one layer of the override file's contents.
// Repo layer must NOT carry API keys; the POST handler rejects them.
type ProjectOverridePayload struct {
	ReasoningProvider *string `json:"reasoning_provider,omitempty"`
	ReasoningModel    *string `json:"reasoning_model,omitempty"`
	VoyageAPIKey      *string `json:"voyage_api_key,omitempty"`
	OpenAIAPIKey      *string `json:"openai_api_key,omitempty"`
	AnthropicAPIKey   *string `json:"anthropic_api_key,omitempty"`

	SyncEnabled                *bool   `json:"sync_enabled,omitempty"`
	SyncDir                    *string `json:"sync_dir,omitempty"`
	SyncLogEmbeddings          *bool   `json:"sync_log_embeddings,omitempty"`
	SyncReuseEmbeddings        *bool   `json:"sync_reuse_embeddings,omitempty"`
	SyncAutoReconcileOnStartup *bool   `json:"sync_auto_reconcile_on_startup,omitempty"`

	DreamingEnabled         *bool   `json:"dreaming_enabled,omitempty"`
	DreamingInterval        *string `json:"dreaming_interval,omitempty"`
	DreamingMinMessages     *int    `json:"dreaming_min_messages,omitempty"`
	DreamingContextMemories *int    `json:"dreaming_context_memories,omitempty"`
	DreamingOnIdleSeconds   *int    `json:"dreaming_on_idle_seconds,omitempty"`
}

// EffectiveProjectConfig is the flat read-only resolved config for a
// project, after stacking repo + user overrides on the global base.
// Shape mirrors ConfigView so the UI can reuse the same form components.
type EffectiveProjectConfig struct {
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	ReasoningProvider string `json:"reasoning_provider"`
	ReasoningModel    string `json:"reasoning_model"`
	VoyageAPIKey      string `json:"voyage_api_key"`
	OpenAIAPIKey      string `json:"openai_api_key"`
	AnthropicAPIKey   string `json:"anthropic_api_key"`
	SyncEnabled       bool   `json:"sync_enabled"`
	SyncDir           string `json:"sync_dir"`

	DreamingEnabled         bool   `json:"dreaming_enabled"`
	DreamingInterval        string `json:"dreaming_interval"`
	DreamingMinMessages     int    `json:"dreaming_min_messages"`
	DreamingContextMemories int    `json:"dreaming_context_memories"`
	DreamingOnIdleSeconds   int    `json:"dreaming_on_idle_seconds"`
}
