package ai

import (
	"fmt"

	"github.com/tomanagle/yullu/internal/config"
)

// MustBuildEmbedder constructs an Embedder and panics on failure. Use in
// process startup where a missing embedder means the service can't run.
//
// yullu requires a cloud embedding provider - there is no local
// option. Voyage (voyage-code-3) is the recommended default; OpenAI
// (text-embedding-3-small) is supported. An API key is mandatory.
func MustBuildEmbedder(cfg config.Config, rec UsageRecorder) Embedder {
	e, err := BuildEmbedder(cfg, rec)
	if err != nil {
		panic(fmt.Errorf("init embedder: %w", err))
	}
	if dim := e.Dim(); dim <= 0 {
		panic(fmt.Errorf("could not determine embedding dimension for %s - check provider connectivity", e.ID()))
	}
	return e
}

// BuildReasoner constructs a Reasoner from the config + a usage recorder.
// A direct Reasoner is optional now that MCP sampling is the primary
// reasoning path - returns (nil, nil) when no direct provider is
// configured. The server uses sampling-via-client when the reasoner is
// nil, and falls back to the direct reasoner for background work where
// no client session exists.
func BuildReasoner(cfg config.Config, rec UsageRecorder) (Reasoner, error) {
	provider := cfg.Reasoning.Provider
	if provider == "" {
		return nil, nil
	}
	model := cfg.Reasoning.Model

	switch provider {
	case "anthropic":
		if cfg.Anthropic.APIKey == "" {
			return nil, fmt.Errorf("anthropic reasoner selected but no API key (set ANTHROPIC_API_KEY or [anthropic].api_key)")
		}
		if model == "" {
			model = "claude-haiku-4-5"
		}
		return NewAnthropic(cfg.Anthropic.APIKey, model, rec), nil
	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai reasoner selected but no API key (set OPENAI_API_KEY or [openai].api_key)")
		}
		if model == "" {
			model = "gpt-4o-mini"
		}
		return NewOpenAIReason(cfg.OpenAI.APIKey, model, rec), nil
	default:
		return nil, fmt.Errorf("unknown reasoning provider %q (expected anthropic|openai, or leave blank to use MCP sampling)", provider)
	}
}

// BuildEmbedder constructs an Embedder from the config + a usage recorder.
func BuildEmbedder(cfg config.Config, rec UsageRecorder) (Embedder, error) {
	provider := cfg.Embedding.Provider
	if provider == "" {
		provider = "voyage"
	}
	model := cfg.Embedding.Model

	switch provider {
	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai embedder selected but no API key (set OPENAI_API_KEY or [openai].api_key)")
		}
		if model == "" {
			model = "text-embedding-3-small"
		}
		return NewOpenAIEmbed(cfg.OpenAI.APIKey, model, rec), nil
	case "voyage":
		if cfg.Voyage.APIKey == "" {
			return nil, fmt.Errorf("voyage embedder selected but no API key (get a free key at https://voyageai.com; set VOYAGE_API_KEY or [voyage].api_key)")
		}
		if model == "" {
			model = "voyage-code-3"
		}
		return NewVoyage(cfg.Voyage.APIKey, model, rec), nil
	default:
		return nil, fmt.Errorf("unknown embedding provider %q (expected voyage|openai)", provider)
	}
}
