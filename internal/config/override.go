package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ConfigOverride mirrors Config but with every field a pointer, so nil
// means "inherit from the lower layer". Two override layers stack on the
// global config:
//
//  1. Repo-committed: <gitRoot>/.yullu/config.toml — team-shared knobs
//     (sync dir, dreaming behaviour). API keys here are refused; they'd
//     leak to teammates via git history.
//  2. User-private: $XDG_CONFIG_HOME/yullu/projects/<sanitized_project_id>.toml —
//     personal overrides (API keys, "don't dream on this hobby repo").
//
// Embedding overrides aren't supported: the SQLite store locks embed_id +
// embed_dim at first write, so a per-project embedder change would corrupt
// the vector index. The schema below deliberately omits the embedding
// section to make this impossible to express.
type ConfigOverride struct {
	Reasoning *ProviderOverride `toml:"reasoning,omitempty"`
	OpenAI    *KeyOverride      `toml:"openai,omitempty"`
	Anthropic *KeyOverride      `toml:"anthropic,omitempty"`
	Voyage    *KeyOverride      `toml:"voyage,omitempty"`
	Sync      *SyncOverride     `toml:"sync,omitempty"`
	Dreaming  *DreamingOverride `toml:"dreaming,omitempty"`
}

// ProviderOverride lets reasoning provider/model be picked per project.
type ProviderOverride struct {
	Provider *string `toml:"provider,omitempty"`
	Model    *string `toml:"model,omitempty"`
}

// KeyOverride wraps the api_key for a single provider section.
type KeyOverride struct {
	APIKey *string `toml:"api_key,omitempty"`
}

// SyncOverride exposes sync knobs that vary per project.
type SyncOverride struct {
	Enabled                *bool   `toml:"enabled,omitempty"`
	Dir                    *string `toml:"dir,omitempty"`
	LogEmbeddings          *bool   `toml:"log_embeddings,omitempty"`
	ReuseEmbeddings        *bool   `toml:"reuse_embeddings,omitempty"`
	AutoReconcileOnStartup *bool   `toml:"auto_reconcile_on_startup,omitempty"`
}

// DreamingOverride exposes every dreaming knob. "Turn dreaming off for my
// hobby repo, keep it on at work" is the canonical use case.
type DreamingOverride struct {
	Enabled         *bool   `toml:"enabled,omitempty"`
	Interval        *string `toml:"interval,omitempty"`
	MinMessages     *int    `toml:"min_messages,omitempty"`
	ContextMemories *int    `toml:"context_memories,omitempty"`
	OnIdleSeconds   *int    `toml:"on_idle_seconds,omitempty"`
}

// Merge applies override onto base in-place semantics — returns a new
// Config with each override pointer (when non-nil) replacing the base's
// value. Nil pointers leave the base value alone.
func Merge(base Config, o ConfigOverride) Config {
	if o.Reasoning != nil {
		if o.Reasoning.Provider != nil {
			base.Reasoning.Provider = *o.Reasoning.Provider
		}
		if o.Reasoning.Model != nil {
			base.Reasoning.Model = *o.Reasoning.Model
		}
	}
	if o.OpenAI != nil && o.OpenAI.APIKey != nil {
		base.OpenAI.APIKey = *o.OpenAI.APIKey
	}
	if o.Anthropic != nil && o.Anthropic.APIKey != nil {
		base.Anthropic.APIKey = *o.Anthropic.APIKey
	}
	if o.Voyage != nil && o.Voyage.APIKey != nil {
		base.Voyage.APIKey = *o.Voyage.APIKey
	}
	if o.Sync != nil {
		if o.Sync.Enabled != nil {
			base.Sync.Enabled = *o.Sync.Enabled
		}
		if o.Sync.Dir != nil {
			base.Sync.Dir = *o.Sync.Dir
		}
		if o.Sync.LogEmbeddings != nil {
			base.Sync.LogEmbeddings = *o.Sync.LogEmbeddings
		}
		if o.Sync.ReuseEmbeddings != nil {
			base.Sync.ReuseEmbeddings = *o.Sync.ReuseEmbeddings
		}
		if o.Sync.AutoReconcileOnStartup != nil {
			base.Sync.AutoReconcileOnStartup = *o.Sync.AutoReconcileOnStartup
		}
	}
	if o.Dreaming != nil {
		if o.Dreaming.Enabled != nil {
			base.Dreaming.Enabled = *o.Dreaming.Enabled
		}
		if o.Dreaming.Interval != nil {
			base.Dreaming.Interval = *o.Dreaming.Interval
		}
		if o.Dreaming.MinMessages != nil {
			base.Dreaming.MinMessages = *o.Dreaming.MinMessages
		}
		if o.Dreaming.ContextMemories != nil {
			base.Dreaming.ContextMemories = *o.Dreaming.ContextMemories
		}
		if o.Dreaming.OnIdleSeconds != nil {
			base.Dreaming.OnIdleSeconds = *o.Dreaming.OnIdleSeconds
		}
	}
	return base
}

// LoadOverride reads an override file from path. Returns a zero-value
// override (no fields set) if the file doesn't exist - missing is the
// normal case for projects that haven't been customised. allowSecrets
// controls whether api_key fields are honoured: callers loading the repo
// layer should pass false, which strips any keys and emits a warning.
//
// The warnings slice is non-nil but possibly empty; callers should log it
// but never block on it.
func LoadOverride(path string, allowSecrets bool) (ConfigOverride, []string, error) {
	var override ConfigOverride
	var warnings []string

	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return override, warnings, nil
	}
	if err != nil {
		return override, warnings, fmt.Errorf("read override %s: %w", path, err)
	}
	if _, err := toml.Decode(string(b), &override); err != nil {
		return override, warnings, fmt.Errorf("parse override %s: %w", path, err)
	}

	if !allowSecrets {
		if override.OpenAI != nil && override.OpenAI.APIKey != nil {
			warnings = append(warnings, fmt.Sprintf("%s: ignoring [openai].api_key (committed files must not carry secrets)", path))
			override.OpenAI.APIKey = nil
		}
		if override.Anthropic != nil && override.Anthropic.APIKey != nil {
			warnings = append(warnings, fmt.Sprintf("%s: ignoring [anthropic].api_key (committed files must not carry secrets)", path))
			override.Anthropic.APIKey = nil
		}
		if override.Voyage != nil && override.Voyage.APIKey != nil {
			warnings = append(warnings, fmt.Sprintf("%s: ignoring [voyage].api_key (committed files must not carry secrets)", path))
			override.Voyage.APIKey = nil
		}
	}

	return override, warnings, nil
}

// WriteOverride serialises override to path, creating parent dirs. Empty
// fields are omitted thanks to the toml:omitempty tags + pointer types, so
// the file only carries the explicitly-set knobs.
func WriteOverride(path string, override ConfigOverride) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(override); err != nil {
		return fmt.Errorf("encode override: %w", err)
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// RepoOverridePath returns the path to the team-shared override file for
// a given git working tree root. Always uses ".yullu/config.toml" regardless
// of what sync_dir is set to — the location of the override file itself
// must not depend on a value it might be overriding.
func RepoOverridePath(gitRoot string) string {
	if gitRoot == "" {
		return ""
	}
	return filepath.Join(gitRoot, ".yullu", "config.toml")
}

// UserOverridePath returns the path to the user-private override for a
// project. project_id can contain slashes ("github.com/tomanagle/Yul-lu"),
// so we sanitise it for use as a filename.
func UserOverridePath(projectID string) string {
	if projectID == "" {
		return ""
	}
	dir, err := userConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "yullu", "projects", sanitizeProjectID(projectID)+".toml")
}

// sanitizeProjectID turns a project_id into a stable filename. We don't
// hash it — we want the file to be human-discoverable so users can edit it
// in $EDITOR. Slashes and colons become double-underscores; everything else
// passes through. URL-shaped IDs ("github.com/tomanagle/Yul-lu") become
// "github.com__tomanagle__Yul-lu".
func sanitizeProjectID(s string) string {
	r := strings.NewReplacer("/", "__", ":", "__", "\\", "__", "?", "_", "*", "_")
	return r.Replace(s)
}

// userConfigDir returns $XDG_CONFIG_HOME or ~/.config, matching the
// convention the rest of the code uses for data paths.
func userConfigDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// Resolve produces the effective config for a project by applying the repo
// + user override layers on top of base. Layers are applied in priority
// order — user (highest) wins ties, then repo, then base. Returns the
// effective config plus any non-fatal warnings (e.g. "repo file had an
// API key, ignored it").
//
// gitRoot may be "" (project isn't in a git repo); the repo layer is
// skipped in that case. projectID may be "" too; both layers are skipped
// and base is returned unchanged.
func Resolve(base Config, gitRoot, projectID string) (Config, []string, error) {
	effective := base
	var warnings []string

	if repoPath := RepoOverridePath(gitRoot); repoPath != "" {
		repo, repoWarns, err := LoadOverride(repoPath, false)
		if err != nil {
			return effective, warnings, err
		}
		warnings = append(warnings, repoWarns...)
		effective = Merge(effective, repo)
	}

	if userPath := UserOverridePath(projectID); userPath != "" {
		user, userWarns, err := LoadOverride(userPath, true)
		if err != nil {
			return effective, warnings, err
		}
		warnings = append(warnings, userWarns...)
		effective = Merge(effective, user)
	}

	return effective, warnings, nil
}
