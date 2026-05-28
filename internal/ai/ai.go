// Package ai defines the abstract AI connector interfaces and shared types.
// Concrete providers (Ollama, OpenAI, Anthropic, Voyage, fastembed) implement
// Embedder, Reasoner, or both.
package ai

import "context"

// Embedder turns text into fixed-size float32 vectors for similarity search.
type Embedder interface {
	// Embed returns one vector per input. All returned vectors share Dim().
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim is the dimensionality of the produced vectors.
	Dim() int
	// ID uniquely identifies the embedder (provider:model). The store uses this
	// to detect when the configured embedder changes against an existing DB.
	ID() string
}

// Reasoner runs a chat-style LLM call. Used by features that need the server
// itself to make judgement calls (dedup, auto-summarization, tag suggestion).
type Reasoner interface {
	Reason(ctx context.Context, req ReasonRequest) (string, error)
	ID() string
}

// Message is one turn in a chat-style prompt.
type Message struct {
	Role    string // "user" or "assistant"
	Content string
}

// ReasonRequest is the input to a Reasoner.
type ReasonRequest struct {
	System    string
	Messages  []Message
	MaxTokens int
}
