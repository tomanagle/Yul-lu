// Package config loads and persists the on-disk configuration for yullu.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk configuration for yullu.
type Config struct {
	Embedding ProviderConfig `toml:"embedding"`
	Reasoning ProviderConfig `toml:"reasoning"`
	OpenAI    KeyedConfig    `toml:"openai"`
	Anthropic KeyedConfig    `toml:"anthropic"`
	Voyage    KeyedConfig    `toml:"voyage"`
	Sync      SyncConfig     `toml:"sync"`
	Dreaming  DreamingConfig `toml:"dreaming"`
}

// DreamingConfig controls the background process that turns recorded
// session messages into durable memories via the reasoner.
type DreamingConfig struct {
	// Enabled gates the entire dreaming feature. record_messages and
	// dream_now still work when disabled, but no background pass runs.
	Enabled bool `toml:"enabled"`
	// Interval is the cadence of the scheduled dream pass (Go duration
	// syntax: "30m", "2h"). Defaults to 30m if unset/invalid.
	Interval string `toml:"interval"`
	// MinMessages is the floor below which a session is left alone by the
	// scheduler. dream_now ignores this and dreams whatever's there.
	MinMessages int `toml:"min_messages"`
	// ContextMemories caps how many existing memories the reasoner sees
	// per dream pass. Larger = better update/delete decisions at the cost
	// of more reasoner tokens.
	ContextMemories int `toml:"context_memories"`
	// OnIdleSeconds, when > 0, fires a dream pass if record_messages has
	// been silent for this many seconds and there are unprocessed messages.
	// 0 disables the idle trigger; interval still applies.
	OnIdleSeconds int `toml:"on_idle_seconds"`
}

// IntervalDuration parses the configured Interval string, falling back to
// 30 minutes on empty / invalid values.
func (d DreamingConfig) IntervalDuration() time.Duration {
	if d.Interval == "" {
		return 30 * time.Minute
	}
	v, err := time.ParseDuration(d.Interval)
	if err != nil || v <= 0 {
		return 30 * time.Minute
	}
	return v
}

// SyncConfig controls cross-developer memory sharing via the .yullu/
// event log committed to the repo.
type SyncConfig struct {
	// Enabled turns event logging on. When false, the server is purely local
	// and never reads or writes the .yullu/ directory.
	Enabled bool `toml:"enabled"`
	// Dir is the directory under the git root that holds the event log.
	// Defaults to ".yullu".
	Dir string `toml:"dir"`
	// LogEmbeddings makes this developer publish their computed vectors so
	// teammates using the same embedder can skip re-embedding.
	LogEmbeddings bool `toml:"log_embeddings"`
	// ReuseEmbeddings makes this developer accept vectors from teammates
	// whose embedder ID matches ours. When false, always recompute locally.
	ReuseEmbeddings bool `toml:"reuse_embeddings"`
	// AutoReconcileOnStartup runs reconcile on server boot so a fresh clone
	// (or one that's been idle while teammates committed events) gets its
	// local DB caught up without explicit user action.
	AutoReconcileOnStartup bool `toml:"auto_reconcile_on_startup"`
}

// ProviderConfig names a provider and its model.
type ProviderConfig struct {
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
}

// KeyedConfig holds an API key for cloud providers.
type KeyedConfig struct {
	APIKey string `toml:"api_key"`
}

// DefaultConfig returns the zero-config defaults. Embedding defaults to
// Voyage (voyage-code-3); reasoning is left empty so the server uses MCP
// sampling - clients with Pro/Plus subscriptions can handle reasoning via
// their existing credentials.
func DefaultConfig() Config {
	return Config{
		Embedding: ProviderConfig{Provider: "voyage"},
		Sync: SyncConfig{
			Enabled:                true,
			Dir:                    ".yullu",
			LogEmbeddings:          true,
			ReuseEmbeddings:        true,
			AutoReconcileOnStartup: true,
		},
		Dreaming: DreamingConfig{
			Enabled:         true,
			Interval:        "30m",
			MinMessages:     10,
			ContextMemories: 50,
			OnIdleSeconds:   0,
		},
	}
}

// MustDefaultPath returns ./config.toml resolved against the current working
// directory (or $YULLU_CONFIG if set). Panics if the cwd can't be
// resolved - without a path, the process can't load config.
func MustDefaultPath() string {
	if env := os.Getenv("YULLU_CONFIG"); env != "" {
		return env
	}
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("resolve config path: %w", err))
	}
	return filepath.Join(cwd, "config.toml")
}

// MustLoad reads the config from path, writing defaults if absent, then
// applies env overrides. Panics on failure - the process can't start without
// a parseable config. Path is injected so callers control where it reads.
func MustLoad(path string) Config {
	cfg, err := loadOrCreate(path)
	if err != nil {
		panic(fmt.Errorf("load config: %w", err))
	}
	return cfg
}

// loadOrCreate reads the config from path, or writes defaults if it doesn't
// exist. Environment variables override the file (see ApplyEnvOverrides).
func loadOrCreate(path string) (Config, error) {
	cfg := DefaultConfig()

	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := WriteDefault(path); err != nil {
			return cfg, fmt.Errorf("write default config: %w", err)
		}
	case err != nil:
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	default:
		if _, err := toml.Decode(string(b), &cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	ApplyEnvOverrides(&cfg)
	return cfg, nil
}

// WriteDefault writes a default config file to path, creating parent dirs.
func WriteDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfigTOML), 0o644)
}

// ApplyEnvOverrides lets env vars trump the file, so users can override
// without editing the config (useful for tests and CI).
func ApplyEnvOverrides(c *Config) {
	if v := os.Getenv("YULLU_EMBED_PROVIDER"); v != "" {
		c.Embedding.Provider = v
	}
	if v := os.Getenv("YULLU_EMBED_MODEL"); v != "" {
		c.Embedding.Model = v
	}
	if v := os.Getenv("YULLU_REASON_PROVIDER"); v != "" {
		c.Reasoning.Provider = v
	}
	if v := os.Getenv("YULLU_REASON_MODEL"); v != "" {
		c.Reasoning.Model = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" && c.OpenAI.APIKey == "" {
		c.OpenAI.APIKey = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" && c.Anthropic.APIKey == "" {
		c.Anthropic.APIKey = v
	}
	if v := os.Getenv("VOYAGE_API_KEY"); v != "" && c.Voyage.APIKey == "" {
		c.Voyage.APIKey = v
	}
	// Normalize provider names to lowercase so config is forgiving.
	c.Embedding.Provider = strings.ToLower(strings.TrimSpace(c.Embedding.Provider))
	c.Reasoning.Provider = strings.ToLower(strings.TrimSpace(c.Reasoning.Provider))
}

const defaultConfigTOML = `# yullu config
# See https://github.com/tomanagle/yullu for details.

[embedding]
# provider: "voyage" | "openai"
# voyage-code-3 is the default - it's code-aware, has a generous free tier,
# and costs around $0.18 per 1M tokens beyond that. Get a key at voyageai.com.
provider = "voyage"
# Leave model = "" to use the provider's default.
model = ""

[reasoning]
# Reasoning powers the dreaming + judging features.
#
# Leave provider = "" (default) to use MCP sampling - your MCP client
# (Claude Code, Codex, etc.) handles the LLM call using whatever credentials
# it has. Users with Claude Pro or ChatGPT Plus pay through their existing
# subscription, no extra API key needed.
#
# Set provider = "anthropic" or "openai" to use a direct API key. This is
# what background dreaming uses - sampling needs an active client session.
provider = ""
model = ""

[openai]
# Leave api_key = "" to read from $OPENAI_API_KEY.
api_key = ""

[anthropic]
# Leave api_key = "" to read from $ANTHROPIC_API_KEY.
api_key = ""

[voyage]
# Leave api_key = "" to read from $VOYAGE_API_KEY.
api_key = ""

[sync]
# Cross-developer memory sharing via the .yullu/ directory in the repo.
# Set enabled = false for a purely local, single-developer experience.
enabled = true
dir = ".yullu"
# Publish your computed embedding vectors so teammates with the same embedder
# can skip re-embedding.
log_embeddings = true
# Reuse logged embeddings when the model matches your current embedder.
# Set to false to always recompute locally.
reuse_embeddings = true
# Pull and apply teammates' events on server startup.
auto_reconcile_on_startup = true

[dreaming]
# Background process: the reasoner reviews recorded session messages and
# extracts durable memories, then the messages are cleaned up.
enabled = true
# How often the scheduled dream pass runs (Go duration: "30m", "2h").
interval = "30m"
# Skip sessions with fewer than this many unprocessed messages.
# dream_now bypasses this floor.
min_messages = 10
# How many existing memories to show the reasoner per dream pass.
context_memories = 50
# If > 0, also fire after record_messages has been quiet for this many
# seconds AND there are unprocessed messages. 0 disables the idle trigger.
on_idle_seconds = 0
`
