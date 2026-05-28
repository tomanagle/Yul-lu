package ai

import "math"

// Prices are USD per 1M tokens. OutputPer1M is unused for embedding models.
// Numbers are approximate; update from each provider's pricing page.
type Prices struct {
	InputPer1M  float64
	OutputPer1M float64
}

// pricing is a best-effort lookup keyed by "provider:model". Missing entries
// mean we record zero cost - the token count is still captured so users can
// reconcile later.
var pricing = map[string]Prices{
	// OpenAI embeddings
	"openai:text-embedding-3-small": {InputPer1M: 0.02},
	"openai:text-embedding-3-large": {InputPer1M: 0.13},
	"openai:text-embedding-ada-002": {InputPer1M: 0.10},
	// OpenAI chat (selection)
	"openai:gpt-4o-mini": {InputPer1M: 0.15, OutputPer1M: 0.60},
	"openai:gpt-4o":      {InputPer1M: 2.50, OutputPer1M: 10.00},
	// Anthropic chat (selection)
	"anthropic:claude-haiku-4-5":  {InputPer1M: 1.00, OutputPer1M: 5.00},
	"anthropic:claude-sonnet-4-6": {InputPer1M: 3.00, OutputPer1M: 15.00},
	"anthropic:claude-opus-4-7":   {InputPer1M: 15.00, OutputPer1M: 75.00},
	// Voyage embeddings
	"voyage:voyage-code-3":  {InputPer1M: 0.18},
	"voyage:voyage-3":       {InputPer1M: 0.06},
	"voyage:voyage-3-lite":  {InputPer1M: 0.02},
	"voyage:voyage-3-large": {InputPer1M: 0.18},
}

// computeCost returns the call cost in USD microcents (10⁻⁶ cent = 10⁻⁸
// dollar). Float math is fine for the intermediate computation - the
// rounding happens at the int64 boundary, so storage and aggregation are
// exact. Returns 0 for models without a pricing entry.
func computeCost(provider, model string, inputTokens, outputTokens int) int64 {
	p, ok := pricing[provider+":"+model]
	if !ok {
		return 0
	}
	const perMillion = 1_000_000.0
	const microcentsPerDollar = 100_000_000.0
	costDollars := (float64(inputTokens)/perMillion)*p.InputPer1M +
		(float64(outputTokens)/perMillion)*p.OutputPer1M
	return int64(math.Round(costDollars * microcentsPerDollar))
}
