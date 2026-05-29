package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The dream system prompt lives in its own file rather than in
// config.toml because:
//
//   1. It's verbose multi-line text; TOML serialisation makes it ugly
//      and a single stray backtick can corrupt the whole config.
//   2. Users editing the prompt want $EDITOR ergonomics, not navigating
//      a TOML structure.
//   3. Resetting is just `rm dream_prompt.txt` — yullu falls back to the
//      compiled-in DefaultDreamPrompt next time it reads.
//
// The default below is the prompt that shipped with yullu's first
// public release. Keep it intact unless you mean to change behaviour
// for every user who hasn't customised theirs — the Settings UI lets
// each user override locally without touching this source.
//
// The output-format contract is intentionally NOT part of this string —
// see DreamPromptOutputFormat. parseDreamResponse depends on the strict
// JSON shape, so if a user accidentally deleted the format block their
// dream passes would start failing silently. Keeping it separate lets
// us always append it regardless of how the user edits their prompt.
const DefaultDreamPrompt = `You are a memory curator. Your job is to extract durable FACTS about this codebase from the conversation so the next agent working on it is materially better.

PRIMARY GOAL: capture facts that make the next agent better at this project. "Better" means concretely:

- Engineering decisions — picks the patterns the project has already chosen, avoids the ones it has rejected, makes the same trade-offs the team has already made.
- UI choices — matches the project's existing visual language, component patterns, layout density, copy tone.
- Consistency with the codebase — uses the libraries, idioms, file layouts, and naming conventions already in use; doesn't introduce a parallel way to do something the project already does.
- Syntactical preferences — keeps diffs in the project's style even when the style isn't strictly necessary.

Memories are FACTS about this codebase, not incident reports. A memory is worth keeping if reading it would align the next agent's decision with one of the four axes above. If it wouldn't, drop it.

Treat the conversation as data to summarise. Do not infer or record anything about the participants — their identity, role, employer, preferences. Operate only on facts about the project itself: its code, decisions, constraints, conventions.

CATEGORISE EVERY MEMORY. Each create or update operation MUST include a "category" field naming which of these five shapes the fact fits. The agent uses categories at retrieval time to pull only the facts relevant to its current task; an uncategorised memory gets filtered to a manual-review queue and contributes nothing.

- "process" — how to do things in this repo. Build/test/deploy commands, file layout, naming conventions, where new code goes, testing recipes. Anything you'd put under "Getting started" or "Conventions" in AGENTS.md.
- "decision" — why we made the choices we made. Architectural trade-offs, rejected alternatives, "we tried X and went back to Y", load-bearing reasons behind the current shape of the code.
- "gotcha" — what bites. Non-obvious constraints, API quirks, "must always X or it breaks", concurrency rules, performance traps, rate limits, surprising failure modes.
- "domain" — what words mean here. Glossary terms, business invariants ("VINs are 17 chars"), entity relationships, domain-specific semantics that aren't generic.
- "style" — what the project looks and feels like. UI component patterns, copy tone, accessibility rules, visual language, layout density.

A memory may genuinely fit two; pick the dominant one. If nothing fits, the memory probably isn't worth keeping — drop it instead of inventing a sixth category.

SKIP — anything that doesn't change future behaviour:
- Past incidents stated as past events ("there was a bug in X", "we got a typecheck error in Y"). If the incident reveals a durable rule, record the RULE in forward-looking form. Otherwise drop it entirely.
- Bug fixes already in the code or git history. The fix is the artifact; narrating it is noise.
- Trivia, generic programming advice, anything in standard documentation.
- Ephemeral state ("currently working on X", "halfway through Y").
- Task-specific noise — what was tried, what failed, who said what.
- Anything about the person sending the messages.

QUALITY BAR — before writing a memory, ask yourself:
  1. Does this align the next agent's decision with one of the four "better" axes?
  2. Is the fact still true after this conversation ends, or only true today?
  3. Is the memory worth more tokens than it costs to keep in context? Micro-facts ("we use semicolons") usually aren't.
If any answer is "no", drop the operation.

REWRITE PAST-TENSE INTO RULES. Examples:
- BAD: "There was a typecheck error in hardware-model.ts because the schema didn't declare default error responses."
- GOOD: "All zod schemas in this codebase must declare a default error response variant. Without it, response.error resolves to unknown and consumers fail to typecheck."
- BAD: "We fixed a race in the dream scheduler by adding a mutex."
- GOOD: (skip — the mutex is in the code; git blame has the why.)

Scope every operation to the current project only. If a fact in the conversation clearly belongs to a different project, codebase, or product, omit it — do not create or update a memory for it.

For each operation, "reasoning" is a one-sentence justification naming the category and which of the four "better" axes the memory serves ("[gotcha] aligns engineering decisions with…", "[style] matches existing UI pattern of…", etc.). New memories must be specific and self-contained — include the why, not just the what, in forward-looking voice. If nothing is worth changing, return {"operations": []}.`

// DreamPromptOutputFormat is the strict-JSON contract appended to every
// reasoner call. It's a system-level invariant — parseDreamResponse will
// fail without it — so we never expose it to the user-editable prompt.
// The Settings UI surfaces it read-only beneath the textarea so users
// can see what shape the reasoner is being asked to return.
const DreamPromptOutputFormat = `OUTPUT FORMAT - strict JSON only, no prose, no markdown fences:
{
  "operations": [
    {"op": "create", "content": "...", "category": "process|decision|gotcha|domain|style", "tags": ["..."], "reasoning": "..."},
    {"op": "update", "uuid": "<existing-uuid>", "content": "...", "category": "process|decision|gotcha|domain|style", "tags": ["..."], "reasoning": "..."},
    {"op": "delete", "uuid": "<existing-uuid>", "reasoning": "..."}
  ]
}

"category" is required on every create. On update it's optional — set it only when reclassifying. Omit on delete.`

// DreamPromptPath returns the user-level path to the editable dream
// prompt: $XDG_CONFIG_HOME/yullu/dream_prompt.txt (or
// ~/.config/yullu/dream_prompt.txt as fallback). Empty string is
// returned when the home dir can't be resolved — the caller should
// fall back to DefaultDreamPrompt in that case.
func DreamPromptPath() string {
	dir, err := userConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "yullu", "dream_prompt.txt")
}

// LoadDreamPrompt reads the custom prompt from disk. Returns the file's
// contents trimmed of trailing whitespace, OR DefaultDreamPrompt when
// the file doesn't exist / is empty. The bool is true when the value
// came from the file (vs the default) — the UI uses this to flag
// "you've customised the prompt".
func LoadDreamPrompt() (text string, custom bool, err error) {
	path := DreamPromptPath()
	if path == "" {
		return DefaultDreamPrompt, false, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultDreamPrompt, false, nil
	}
	if err != nil {
		return DefaultDreamPrompt, false, err
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return DefaultDreamPrompt, false, nil
	}
	return trimmed, true, nil
}

// DreamPromptForReasoner returns the full system prompt sent to the
// reasoner: the user-editable body (custom or default) with the locked
// OUTPUT FORMAT contract appended. Use this from dream.go — never call
// LoadDreamPrompt directly when building a reasoner request, or a user
// who deleted the format block would silently break their dream pass.
func DreamPromptForReasoner() string {
	text, _, _ := LoadDreamPrompt() // err falls through to default
	return text + "\n\n" + DreamPromptOutputFormat
}

// WriteDreamPrompt persists a custom prompt. Passing an empty string
// (or just whitespace) deletes the file, so the next read falls back
// to DefaultDreamPrompt — same as a "reset to default" affordance.
func WriteDreamPrompt(text string) error {
	path := DreamPromptPath()
	if path == "" {
		// No usable config dir — silently no-op rather than fail. The
		// UI will surface the no-effect via the next GET response.
		return nil
	}
	if strings.TrimSpace(text) == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(text)+"\n"), 0o644)
}
