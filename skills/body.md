# Yul'lu - codebase memory

Yul'lu (https://yullu.ai) gives you persistent, semantically-searchable
memory scoped to the current codebase. Conversations in this repo write to
and read from a shared memory store; if team sync is enabled, your teammates'
memories are available too.

Memories are scoped automatically by the git remote URL of the working
directory - you don't need to pass `project_id` unless you're operating
across multiple projects in one session.

## When to use which tool

### `retrieve_memories` - before answering codebase questions

Before you investigate the code, call `retrieve_memories` whenever the
user asks something that depends on the codebase's history, conventions,
or past decisions. Use a natural-language query phrased like the question.

Examples:

- User: "Why is the auth middleware structured this way?"
  → `retrieve_memories(query="auth middleware design rationale")`
- User: "Should I use Postgres or SQLite here?"
  → `retrieve_memories(query="database choice")`
- User: "How do we handle migrations?"
  → `retrieve_memories(query="migration policy and conventions")`

You do not need to announce that you searched. Use what comes back as
context and answer normally. If nothing relevant returns, fall through to
your usual investigation.

### `store_memory` - when you learn something durable

When the user makes a decision, explains the *why* behind code, describes
a gotcha, or shares a fact that would help a future session, call
`store_memory`. Don't ask permission - just save it.

Worth saving:

- Decisions and their rationale ("We chose Bun over Node because…")
- Constraints that aren't obvious from the code ("Auth tokens must be <4KB
  or the gateway drops them")
- Reversed decisions ("Migration 0042 was reverted because…")
- Team conventions that aren't documented ("PRs require a test plan,
  bug fixes don't need one")
- Forward-looking context ("We're moving to RSC next quarter")

Not worth saving:

- Transient state ("running the tests now", "let me check that file")
- Things obvious from the code itself
- Per-turn meta-commentary

Each memory should be self-contained - assume the reader has zero context
from this conversation. Include the *why*, not just the *what*.

```
store_memory(
  content="The dreamer must hold dreamMu before reading session_messages; concurrent dreams race on the read-then-delete step. See server/dream.go.",
  tags=["dreaming", "concurrency"]
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
  messages=[
    {"role": "user", "content": "<the user's last message verbatim>"},
    {"role": "assistant", "content": "<your reply verbatim>"}
  ]
)
```

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
published to `.yullu/events/` or shared with teammates. Only the durable
memories the dream pass extracts get committed.

### Other tools

- `update_memory(id, content, tags)` - when the user corrects something
  you stored, or when a decision changes.
- `delete_memory(id)` - remove a memory that turned out to be wrong.
- `list_memories(limit)` - overview of recent memories. Useful at the
  start of a session if `retrieve_memories` returns nothing for the
  user's question.
- `dream_now()` - trigger a dream pass immediately. Rarely needed; the
  scheduler handles it.
- `reconcile_memories()` - pull teammate-committed events from
  `.yullu/events/` and publish any local-only memories. Run after
  `git pull` if you suspect teammates have new memories.

## Workflow per session

1. **First substantive question**: silently call `retrieve_memories` with
   the user's query before answering. Reuse what comes back as context.
2. **During the session**: when learnings emerge, call `store_memory`
   without asking permission.
3. **After every substantive turn** (see `record_messages` section above
   for the exact bar): call `record_messages` with the user/assistant
   exchange, using a stable `session_id` for the whole chat. **This is
   the step that's easiest to forget and the one Yul'lu needs most** -
   the dream pass can't extract memories from messages it never saw.

## Heuristics

- **When in doubt, store.** The dreamer collapses duplicates and updates
  outdated memories on its next pass.
- **Search before assuming.** Even if you think you know the answer, a
  retrieve_memories call costs nothing locally and may surface a prior
  decision that contradicts what you would have said.
- **Phrase memories as standalone notes.** Someone reading the memory in
  six months - possibly a different assistant on a different machine -
  should be able to act on it without conversational context.
