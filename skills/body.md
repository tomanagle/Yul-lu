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

### `record_messages` - after every substantive turn

After each user message + your reply, call `record_messages` with the
exchange. The dreamer will later extract durable memories from these
conversations in the background, so you don't have to flag every learning
in real time.

```
record_messages(
  session_id="<a stable string identifying this chat>",
  messages=[
    {"role": "user", "content": "<the user's last message>"},
    {"role": "assistant", "content": "<your reply>"}
  ]
)
```

Use the same `session_id` across all turns within one conversation. A
UUID, a timestamp, or any unique-per-chat string works.

`record_messages` is best-effort. If it fails, log it and continue -
don't surface the error to the user.

Skip it for trivial exchanges (greetings, confirmations, "thanks").

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
   the user's query before answering.
2. **During the session**: when learnings emerge, call `store_memory`
   without asking.
3. **After each non-trivial turn**: call `record_messages` with the
   user/assistant exchange.

## Heuristics

- **When in doubt, store.** The dreamer collapses duplicates and updates
  outdated memories on its next pass.
- **Search before assuming.** Even if you think you know the answer, a
  retrieve_memories call costs nothing locally and may surface a prior
  decision that contradicts what you would have said.
- **Phrase memories as standalone notes.** Someone reading the memory in
  six months - possibly a different assistant on a different machine -
  should be able to act on it without conversational context.
