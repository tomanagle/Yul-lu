package ai

import (
	"context"
	"time"
)

// Kind names the role of a model call recorded in the usage table.
const (
	KindEmbed  = "embed"
	KindReason = "reason"
)

// UsageEvent describes a single call to an external (or in-process) model.
// Every Embedder/Reasoner records one event per call.
type UsageEvent struct {
	At           time.Time
	Provider     string // "ollama", "openai", "anthropic", "voyage", "fastembed"
	Model        string // raw provider model name
	Kind         string // KindEmbed | KindReason
	InputTokens  int    // best-effort; 0 if the provider doesn't report it
	OutputTokens int    // 0 for embeddings
	// CostMicrocentsUSD is the call cost in USD microcents (10⁻⁶ cent =
	// 10⁻⁸ dollar). Stored as int64 so accumulated totals don't suffer
	// float drift, and granular enough to capture sub-cent per-call costs
	// (e.g. ~20 microcents for a small embedding). 0 for local providers.
	CostMicrocentsUSD int64
	LatencyMs         int64
	Items             int  // number of texts (embed) or messages (reason)
	OK                bool // false if the call failed
	ErrorMsg          string
}

// UsageRecorder is implemented by anything that can persist usage events -
// typically the SQLite store. Providers depend on this interface, not on
// the store directly, to avoid an import cycle.
type UsageRecorder interface {
	RecordUsage(ctx context.Context, e UsageEvent) error
}

// nopRecorder discards events. Used when the caller doesn't pass a recorder
// (tests, or when usage tracking is intentionally disabled).
type nopRecorder struct{}

func (nopRecorder) RecordUsage(context.Context, UsageEvent) error { return nil }

// NopRecorder returns a recorder that discards events.
func NopRecorder() UsageRecorder { return nopRecorder{} }

// RecorderRef is a UsageRecorder whose underlying recorder can be swapped at
// runtime. Useful when the model providers must be constructed before the
// store (which is itself the recorder) is open.
type RecorderRef struct {
	inner UsageRecorder
}

// NewRecorderRef returns a Ref initially backed by a nop recorder.
func NewRecorderRef() *RecorderRef { return &RecorderRef{inner: nopRecorder{}} }

// Set replaces the underlying recorder.
func (r *RecorderRef) Set(rec UsageRecorder) {
	if rec == nil {
		r.inner = nopRecorder{}
		return
	}
	r.inner = rec
}

// RecordUsage forwards to the current inner recorder.
func (r *RecorderRef) RecordUsage(ctx context.Context, e UsageEvent) error {
	return r.inner.RecordUsage(ctx, e)
}

// recordSafely calls rec.RecordUsage and swallows errors. Recording must
// never fail the underlying model call.
func recordSafely(ctx context.Context, rec UsageRecorder, e UsageEvent) {
	if rec == nil {
		return
	}
	_ = rec.RecordUsage(ctx, e)
}

// estimateTokens is a crude char/4 fallback when the provider doesn't return
// a usage block. Better than nothing for back-of-envelope cost math.
func estimateTokens(texts ...string) int {
	total := 0
	for _, t := range texts {
		total += len(t)
	}
	if total == 0 {
		return 0
	}
	tok := total / 4
	if tok < 1 {
		tok = 1
	}
	return tok
}
