# Yul'lu - codebase memory

Yul'lu gives you persistent, semantically-searchable
memory scoped to the current codebase. Conversations in this repo write to
and read from a shared memory store; if team sync is enabled, your teammates'
memories are available too.

Memories are scoped automatically by the git remote URL of the working
directory - you don't need to pass `project_id` unless you're operating
across multiple projects in one session.

## Memory categories — the key to using Yul'lu well

Every memory is classified into one of five categories. **You use categories
both ways**: when you store a memory, you set one; when you retrieve, you
filter to the ones that match your current task. Filtering at retrieval is
the difference between "the agent has a memory system" and "the agent
actually behaves differently." Pull only what you need.

- **`process`** — how to do things in this repo. Build/test/deploy commands,
  file layout, naming conventions, where new code goes, testing recipes.
  Anything you'd put under "Getting started" or "Conventions" in AGENTS.md.
- **`decision`** — why we made the choices we made. Architectural
  trade-offs, rejected alternatives, "we tried X and went back to Y."
- **`gotcha`** — what bites. Non-obvious constraints, API quirks,
  "must always X or it breaks", concurrency rules, performance traps,
  surprising failure modes.
- **`domain`** — what words mean here. Glossary terms, business invariants
  ("VINs are 17 chars"), entity relationships, domain semantics.
- **`style`** — what the project looks and feels like. UI component
  patterns, copy tone, accessibility rules, visual language.

## When to use which tool

### `retrieve_memories` — call this at every task boundary

Before any substantive action in this repo — writing code, changing existing
code, running a command, answering a project-specific question — ask
yourself **"what category of facts would change my approach?"** and call
`retrieve_memories` with those categories. The categories give you a
checklist; the open-ended question "should I look something up?" gets
skipped, the checklist gets followed.

**Category checklist** — match the task to one or more:

| You're about to… | Categories to fetch |
|---|---|
| Write new code / add a feature | `process`, `style` (if it touches UI) |
| Modify existing code | `process`, `decision`, `gotcha` |
| Refactor or restructure | `decision`, `gotcha` |
| Add or change a UI component | `style`, `process` |
| Fix a bug / debug | `gotcha`, `decision` |
| Run an unfamiliar command | `process` |
| Answer "what does X mean here?" | `domain` |
| Answer "why is X like this?" | `decision` |

Pass multiple categories when in doubt — the SQL is OR'd, returns are
ranked by similarity. Skip the call only for genuinely categorical work
(a typo fix, a one-line tweak, a question with no project-specific answer).

Examples:

- User: "Add a settings tab for project overrides"
  → `retrieve_memories(query="settings tab project overrides", categories=["process", "style"], cwd=...)`
- User: "Why is auth middleware structured this way?"
  → `retrieve_memories(query="auth middleware structure", categories=["decision", "gotcha"], cwd=...)`
- User: "How do we handle migrations?"
  → `retrieve_memories(query="migrations conventions", categories=["process", "decision"], cwd=...)`
- User: "What's a 'session_id' in this codebase?"
  → `retrieve_memories(query="session_id definition", categories=["domain"], cwd=...)`

You do not need to announce that you searched. Use what comes back as
context and answer normally. If nothing returns, fall through to your
usual investigation — empty results mean "no project-specific knowledge
recorded yet," not "your task is invalid."

**Don't omit `categories`** unless you genuinely don't know which to ask
for. A filtered query is faster, returns more relevant results, and
costs fewer tokens than dumping every shape of memory into your context.

### `store_memory` — when you learn something durable

When the user makes a decision, explains the *why* behind code, describes
a gotcha, or shares a fact that would help a future session, call
`store_memory`. Don't ask permission — just save it.

**Always classify with `category`.** The categorisation is what makes the
memory retrievable later by a task-aware query. An uncategorised memory
gets dropped into a manual-review queue and contributes nothing until a
human triages it. Pick the dominant category if a memory could fit two.

Worth saving (with example categories):

- Architectural choices and their rationale → **`decision`** ("We chose
  Bun over Node because the test suite needs sub-second cold start.")
- Non-obvious constraints → **`gotcha`** ("Auth tokens must be <4KB or
  the gateway drops them silently.")
- Build/test/deploy commands and conventions → **`process`** ("Tests are
  `bun test`; coverage runs only in CI via `make coverage`.")
- Project-specific terms / invariants → **`domain`** ("A 'pass' here
  means one full dreamer cycle over all buffered session messages.")
- UI patterns / visual rules → **`style`** ("All cards in this app use
  `border-border/40 bg-card/60` for the muted-glass look — don't reach
  for raw `bg-card`.")

Not worth saving:

- Transient state ("running the tests now", "let me check that file")
- Things obvious from the code itself
- Per-turn meta-commentary
- Past-tense incidents — record the **rule** they reveal, not the event

Each memory should be self-contained — assume the reader has zero context
from this conversation. Include the *why*, not just the *what*.

```
store_memory(
  content="The dreamer must hold dreamMu (single-flight TryLock) before reading session_messages, because concurrent dreams race on the read-then-delete step.",
  category="gotcha",
  tags=["dreaming", "concurrency"],
  cwd="<your current working directory>"
)
```

### `record_messages` - REQUIRED after every substantive turn

**This is the most important Yul'lu tool to use consistently.** The dream
pass that extracts memories cannot run without recorded messages, so if
you never call `record_messages`, Yul'lu cannot get better over time. Treat
it as part of every response, not as an optional extra.

**When to call it:** after every user-assistant exchange where the user
said something with content - a request, a decision, a clarification, an
opinion, a reaction. Call it as one of the last actions of the turn,
*after* you've answered. Do not announce that you're recording.

**What counts as substantive (call `record_messages`):**

- The user asks for any change to code, config, or docs.
- The user explains *why* they want something or pushes back on an idea.
- The user reports a bug, surfaces a constraint, or names a tradeoff.
- The user expresses a preference about how to work ("prefer Bun over npm").
- The user mentions a person, deadline, system, or external dependency.
- You delivered a non-trivial answer (more than one sentence of substance).

**What to skip (don't call `record_messages`):**

- Pure pleasantries with no content ("thanks", "ok", "cool", "👍").
- Single-word acknowledgements where you didn't reply with substance.
- Turns that were entirely *you* recovering from your own mistake with no
  new user input ("retry", "try again").

When in doubt, **call it**. The dream pass discards uninteresting messages
and extracts only durable memories; over-recording is cheap, under-recording
loses signal forever.

**Shape of the call:**

```
record_messages(
  session_id="<stable string for this chat - same across all turns>",
  cwd="<your current working directory — pass it every call>",
  messages=[
    {"role": "user", "content": "<the user's last message verbatim>"},
    {"role": "assistant", "content": "<your reply verbatim>"}
  ]
)
```

**Always pass `cwd`** (and the same goes for `store_memory`, `retrieve_memories`,
`list_memories`, `dream_now`). Yul'lu runs as a single long-lived process —
its own working directory is wherever you launched the binary from, NOT
your repo. Without `cwd`, every call falls back to the server's directory
and ends up scoped to the wrong project. Pass the absolute path of the
project you're working in.

**Session ID rules:**

- Pick a `session_id` on your first call in a chat and reuse it for every
  subsequent `record_messages` in the same chat. A UUID, an ISO timestamp,
  or `chat-<timestamp>-<short-hash>` all work.
- If you don't remember the session_id from earlier in the chat, generate
  a fresh one and use it consistently from this turn onward - the dream
  pass groups by session_id, so consistency matters within a session, not
  across sessions.

**Failure handling:** `record_messages` is best-effort. If it errors, log
once and continue with the user's actual task - never surface the
recording error to the user, and never let it block your reply.

**Heads-up:** raw recorded messages stay local-only - they are never
published to `.yullu/logs/` or shared with teammates. Only the durable
memories the dream pass extracts get committed.

### Other tools

- `update_memory(id, content, tags)` - when the user corrects something
  you stored, or when a decision changes.
- `delete_memory(id)` - remove a memory that turned out to be wrong.
- `list_memories(limit)` - overview of recent memories. Useful at the
  start of a session if `retrieve_memories` returns nothing for the
  user's question.
- `reconcile_memories()` - pull teammate-committed events from
  `.yullu/logs/` and publish any local-only memories. Run after
  `git pull` if you suspect teammates have new memories.

### `dream_now` - fire it when the buffer fills

Yul'lu has a `dream_now` MCP tool that drains the recorded session
messages and extracts memories. Whether to call it depends on how the
user has configured reasoning:

- **API-key mode** (config has `[reasoning].provider = "anthropic"` or
  `"openai"` with a key): a background scheduler already runs dreams on
  a cadence. You do NOT need to call `dream_now` proactively — calling
  it adds reasoner cost without much benefit. Skip unless the user asks.
- **MCP sampling mode** (`[reasoning].provider = ""`, no API key
  configured): **you are the scheduler**. The background dreamer cannot
  run — sampling needs an active client session, which only exists when
  YOU call MCP tools. So the user's memories will not flow unless you
  trigger them.

In sampling mode, call `dream_now` (no arguments needed) when **any** of:

- You've just called `record_messages` and the buffer is now ≥ 20
  messages OR ≥ 5 sessions. (`record_messages` returns the new total
  count — use that.)
- It's been more than ~30 minutes since you last called `dream_now` in
  this conversation, regardless of buffer size.
- The user explicitly asks you to "dream", "process memories", or
  similar.
- You're about to wrap a session (a goodbye, "thanks", or any signal
  the conversation is ending) and the buffer is non-empty.

`dream_now` is the same protocol shape as `record_messages` — fire and
move on. The user sees nothing unless they ask. If sampling fails (the
LLM call errors), Yul'lu falls back to the configured direct reasoner
(possibly none) and surfaces the error in the tool result; either way,
don't surface that to the user unless they ask why memories aren't
appearing.

**How to know which mode you're in**: call `dream_now` once at the
start of a fresh session. If it succeeds, you're either in API-key
mode or sampling works. If it returns `{operations: [], errors: [...]}`
with a "no reasoner available" message, sampling failed and there's no
API key — recording still works (the buffer fills) but the user needs
to add an API key or accept that dreams won't run. Either way, keep
calling `record_messages` as usual.

## Workflow per session

1. **At every task boundary**: silently call `retrieve_memories` with a
   query phrased from the task AND `categories` matching the work
   (see the table above). Reuse what comes back as context. Don't wait
   for a "first substantive question" — call it whenever you switch
   tasks, not just at session start.
2. **During the session**: when learnings emerge, call `store_memory`
   with a `category` set. Don't ask permission, don't leave the category
   off "to let the user pick later" — categorise at write time or the
   memory is dead weight.
3. **After every substantive turn** (see `record_messages` section above
   for the exact bar): call `record_messages` with the user/assistant
   exchange, using a stable `session_id` for the whole chat. **This is
   the step that's easiest to forget and the one Yul'lu needs most** -
   the dream pass can't extract memories from messages it never saw.
4. **When the buffer crosses the threshold** (≥ 20 messages OR ≥ 5
   sessions OR ~30 min since last dream): call `dream_now`. In MCP
   sampling mode this is how memories actually get extracted — there
   is no other scheduler. In API-key mode you can skip this; the
   background dreamer already handles it.

## Heuristics

- **Filter retrieval by category.** A filtered query returns 5 highly-
  relevant memories instead of 5 broadly-related ones. An unfiltered
  query is a smell — it usually means you didn't think about what
  category of fact the task needs.
- **When in doubt, store** — but always with a category. The dreamer
  collapses duplicates and updates outdated memories on its next pass.
- **Search before assuming.** Even if you think you know the answer, a
  `retrieve_memories` call costs nothing locally and may surface a prior
  decision that contradicts what you would have said.
- **Phrase memories as standalone notes.** Someone reading the memory in
  six months — possibly a different assistant on a different machine —
  should be able to act on it without conversational context.
- **Past-tense incidents become forward-tense rules.** "We had a bug in X"
  is not a memory; "X must always do Y because doing Z silently breaks"
  is. If you can't rewrite an event as a rule, don't store it.
