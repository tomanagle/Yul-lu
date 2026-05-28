# Yul'lu

Persistent, semantically-searchable memory for LLMs - scoped per codebase,
syncable across a team via a `.yullu/` directory committed to the repo.
Ships as a single Go binary that serves a web UI, a REST API, and the MCP
endpoint on `localhost:47823`. Works with any MCP client (Claude Code,
Codex, Cursor, …).

## Quick start

```bash
make start
```

That single command:
1. Builds the React frontend (`bun run build`) and embeds it into the binary.
2. Builds + installs `yullu` to `$GOPATH/bin`.
3. Launches the server - open <http://localhost:47823> in your browser for
   the UI, point MCP clients at `http://localhost:47823/mcp`.

**Requirements:**
- **Go 1.25+** and a working **cgo** toolchain (Xcode CLT on macOS).
- **Bun** for the frontend (`brew install oven-sh/bun/bun`, see <https://bun.sh>).
- A **Voyage API key** for embeddings - paste it into the Settings page on
  first launch (free tier at <https://voyageai.com>).

Reasoning happens via **MCP sampling** by default - your client (Claude
Code, Codex) does the LLM call using whatever credentials it has, so
Pro/Plus subscriptions cover it without a separate API key.

Register `yullu` with Claude Code (once `make start` is running):

```bash
claude mcp add yullu --transport http http://localhost:47823/mcp
```

Other handy commands:

```bash
make dev      # vite on :5173 + air-rebuilt Go server on :47823 (HMR)
make smoke    # round-trips initialize + tools/list over stdio
make test     # runs the Go test suite
make install  # install the standalone stdio CLI (no UI) for clients that prefer stdio
make serve    # standalone CLI in HTTP-only mode, live-reloaded by air
```

## What it does

When an LLM learns something durable about a codebase - a decision, a
gotcha, a one-off fact, the reason behind a design - it calls
`store_memory`. Future sessions in the same repo call `retrieve_memories`
to pull the relevant ones back. Memories are:

- **Scoped automatically** by the repo's `origin` remote URL (canonicalised
  so SSH and HTTPS clones resolve to the same project), or the git-root
  path / CWD as fallbacks.
- **Indexed by embedding vector** in SQLite via `sqlite-vec`, so retrieval
  is semantic, not keyword.
- **Optionally synced across teammates** via an event log committed to the
  repo at `<repo>/.yullu/events/`.
- **Optionally dreamed** - the server can review recorded conversation
  turns and extract durable memories without the LLM having to call
  `store_memory` explicitly.

## Reasoning via MCP sampling

Yul'lu uses **MCP sampling** for reasoning by default: when the
server needs an LLM (for dreaming, for example), it asks the *client*
(Claude Code, Codex, etc.) to make the call. The client handles the
credentials and billing - so users with **Claude Pro** or **ChatGPT Plus**
pay through their existing subscription with no extra setup.

Set `[reasoning].provider = "anthropic"` or `"openai"` with an API key
to also enable **background dreaming** - scheduled passes don't have an
active client session to sample from, so they need a direct provider.
Without a direct provider, background dreaming is a no-op; foreground
`dream_now` still works fine via sampling.

## Configuration

A `config.toml` is read from the current directory on every server start
(one per project). Defaults are written on first run. Override the location
with `$YULLU_CONFIG`.

```toml
[embedding]
# provider: "voyage" | "openai". voyage-code-3 is the default - code-aware,
# generous free tier. Get a key at https://voyageai.com.
provider = "voyage"
model = ""       # blank uses provider default

[reasoning]
# Blank = use MCP sampling (client handles the LLM call using its credentials).
# "anthropic" or "openai" with an API key also enables background dreaming.
provider = ""
model = ""

[openai]
api_key = ""     # blank reads $OPENAI_API_KEY

[anthropic]
api_key = ""     # blank reads $ANTHROPIC_API_KEY

[voyage]
api_key = ""     # blank reads $VOYAGE_API_KEY

[sync]
enabled = true                    # write/read .yullu/events
dir = ".yullu"
log_embeddings = true             # publish your computed vectors
reuse_embeddings = true           # accept teammates' vectors when model matches
auto_reconcile_on_startup = true  # apply teammates' events on boot

[dreaming]
enabled = true                    # background memory extraction from chats
interval = "30m"                  # scheduled dream cadence
min_messages = 10                 # scheduler skips sessions smaller than this
context_memories = 50             # how many memories the reasoner sees per pass
on_idle_seconds = 0               # also dream after N idle seconds (0 = off)
```

### Environment variables

| variable | what it overrides |
|---|---|
| `YULLU_CONFIG` | path to `config.toml` |
| `YULLU_DB` | path to the SQLite database |
| `YULLU_LOG_LEVEL` | `debug` / `info` / `warn` / `error` (default `info`) |
| `YULLU_EMBED_PROVIDER` / `_MODEL` | embedding provider/model |
| `YULLU_REASON_PROVIDER` / `_MODEL` | reasoning provider/model |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `VOYAGE_API_KEY` | API keys |

The desktop server always listens on `:47823`. The standalone stdio CLI
also accepts `YULLU_TRANSPORT=http` and
`YULLU_HTTP_ADDR=:8080` for clients that don't want a UI.

### Storage paths

- **Database**: `$YULLU_DB`, else `$XDG_DATA_HOME/yullu/memories.db`,
  else `~/.local/share/yullu/memories.db`. One DB per user, shared
  across every repo you use the binary in - rows are scoped by `project_id`.
- **Event log**: `<repo>/.yullu/events/`, one JSON file per event. This
  is what teammates see; the database is per-machine.

## MCP tools

| tool | purpose |
|---|---|
| `store_memory` | Save a memory. Embeds + scopes to project. Returns the local `id` and the cross-machine `uuid`. |
| `retrieve_memories` | Semantic search over the current project's memories. |
| `update_memory` | Patch content and/or tags by local `id`. Re-embeds if content changed. |
| `delete_memory` | Delete by local `id`. |
| `list_memories` | Recently updated memories for the current project. Useful as an overview. |
| `reconcile_memories` | Pull events from `.yullu/` and publish any local-only rows. Safe to run repeatedly. |
| `record_messages` | Push conversation turns into the dream buffer so the server can later extract memories from them. |
| `dream_now` | Trigger an immediate dream pass over recorded session messages. |
| `get_usage` | Aggregate model usage (calls, tokens, cost, latency in USD microcents) by provider+model+kind. |

## Team sync via `.yullu/`

When `[sync].enabled = true`, the server treats `<repo>/.yullu/events/`
as an append-only log of memory mutations.

**What's in it.** One JSON file per event. Filenames are time-sortable with
nanosecond precision. Event types:

- `create` - content + tags for a new memory (`memory_id` is a UUID).
- `update` - partial patch (omit `content` or `tags` to leave unchanged).
- `delete` - tombstones the memory.
- `embedding` - an embedding vector tagged with the model that produced it,
  so teammates on the same model can skip re-embedding.

**Multi-developer flow.**

1. Alice calls `store_memory`. The server writes a `create` event, embeds
   locally, inserts into her DB, and (if `log_embeddings`) writes an
   `embedding` event tagged with her embedder ID.
2. Alice commits and pushes the `.yullu/` changes.
3. Bob pulls. On next server start (auto-reconcile) or via
   `reconcile_memories`, his server reads Alice's events. Same embedder ID:
   Bob's DB picks up Alice's vector - zero embed calls. Different embedder:
   Bob embeds locally and writes a new `embedding` event for his model, so
   the next teammate using that model gets the free ride.

**Reconcile is idempotent.** Per-project watermarks in the DB (`meta` table
key `last_event:<project_id>`) track which event filenames have been
processed. Re-running reconcile does nothing if no new events have arrived.

**Privacy.** Events are committed to the repo. If the repo is public, the
memories are public. Treat `.yullu/` like documentation - review the
diff before pushing.

## Dreaming

Most memories don't get explicitly flagged by the user - they emerge from
the conversation itself ("oh by the way, we use Bun here", "that migration
was reverted because of X"). **Dreaming** is the background process that
extracts those memories without the LLM (or the human) having to call
`store_memory` explicitly.

**The loop:**

1. The LLM calls `record_messages` after each turn (or in batches), pushing
   `{session_id, [{role, content}, …]}` into a local `session_messages`
   table. The raw text never leaves the local DB - it's not written to
   `.yullu/events/`.
2. On a schedule (or on demand via `dream_now`), the server pulls
   unprocessed messages for each session plus the most recently updated
   memories for the project, and asks the **reasoner** to return a JSON
   list of operations:

   ```json
   {
     "operations": [
       {"op": "create", "content": "…", "tags": ["…"], "reasoning": "…"},
       {"op": "update", "uuid": "<existing>", "content": "…", "reasoning": "…"},
       {"op": "delete", "uuid": "<existing>", "reasoning": "…"}
     ]
   }
   ```

3. Each op is applied via the same write path as a direct LLM call - so
   dreamed memories show up in `.yullu/events/` and propagate to
   teammates exactly like memories created by `store_memory`.
4. Processed messages are deleted from the local DB. Memories live; the
   conversation that produced them does not.

**When dreaming fires:**

- **Interval**: every `[dreaming].interval` (default `30m`). The first
  pass runs on the first tick after server start, so messages pushed
  before boot get processed promptly.
- **Idle** (optional): if `[dreaming].on_idle_seconds > 0`, also fires
  when `record_messages` has been silent for that many seconds and there
  are unprocessed messages. Off by default.
- **Manual**: `dream_now` MCP tool. Bypasses the `min_messages` floor.

**Failure handling:**

- Reasoner network error → session is reported in the result's `errors`,
  messages remain in the buffer for the next pass.
- Reasoner returns prose with no JSON, or malformed JSON → same outcome.
  We log the response excerpt and keep the messages.
- An individual op fails to apply (e.g. `update` for a UUID that doesn't
  exist locally) → that op is counted as skipped, the rest still apply.
  Messages are deleted after the apply pass regardless.

**Single-flight.** Concurrent dreams (a scheduled tick during a slow
`dream_now`) are serialised; the late caller returns `{skipped: true}`
immediately rather than queuing.

**Tuning knobs in `[dreaming]`:**

| key | default | what it does |
|---|---|---|
| `enabled` | `true` | Turn the scheduler on/off. `record_messages` and `dream_now` work regardless. |
| `interval` | `"30m"` | Go duration between scheduled dreams. |
| `min_messages` | `10` | Scheduler skips sessions with fewer messages. `dream_now` ignores this. |
| `context_memories` | `50` | How many existing memories the reasoner sees per pass. Bigger = better update/delete decisions, more tokens. |
| `on_idle_seconds` | `0` | If > 0, also dream after this many seconds of `record_messages` silence. |

## Makefile

Run `make help` to see the list.

| target | what it does |
|---|---|
| `start` | Build the frontend, build + install `yullu`, run the desktop server at `:47823`. |
| `dev` | HMR dev loop: Vite on `:5173` (proxies `/api` and `/mcp` to the Go server) + `air` rebuilds the Go binary on change. Open <http://localhost:5173> during dev. |
| `install-app` | Build the frontend and install `yullu` to `$GOPATH/bin` without running it. |
| `build` | Compile the standalone stdio MCP CLI to `./bin/yullu`. |
| `install` | Install the standalone stdio CLI to `$GOPATH/bin`. |
| `setup` | `install` + print the `claude mcp add` command. |
| `serve` | Run the standalone CLI in HTTP-only mode under `air`. |
| `register` | Print the `claude mcp add yullu …` command for the desktop server. |
| `smoke` | End-to-end stdio round-trip: initialize + tools/list (no API key needed). |
| `test` | `go test ./...` |
| `tidy` | `go mod tidy` |
| `fmt` | `gofmt -s -w .` |
| `vet` | `go vet ./...` |
| `clean` | Remove `./bin` and `cmd/app/frontend/dist`. |

## How it works

```
cmd/
├─ main.go               standalone stdio/HTTP MCP CLI (no UI)
├─ app/                  desktop server - single binary that serves the UI,
│  │                     REST API, and MCP endpoint on :47823
│  ├─ main.go            net/http mux: / (embedded frontend), /api/*, /mcp
│  ├─ app.go             App struct - methods shared between REST + MCP
│  ├─ api.go             JSON REST handlers (one per App method)
│  └─ frontend/          React + TanStack + shadcn (built into the binary)
└─ internal/
   ├─ applog             structured logger configuration (slog + JSON)
   ├─ config             loads config.toml, applies env overrides
   ├─ ai                 Embedder / Reasoner interfaces + providers
   │  ├─ voyage, openai (embedding); anthropic, openai (reasoning)
   │  └─ per-call usage tracking → SQLite usage table
   ├─ store              SQLite + sqlite-vec, schema migration, CRUD; also
   │                     holds the session_messages dream buffer
   ├─ scope              resolves project_id from git remote / path
   ├─ memlog             event log writer + reader for .yullu/events/
   └─ server             MCP tool handlers, reconcile + dream algorithms.
                         Server.callReasoner tries MCP sampling first, then
                         falls back to the configured direct Reasoner.
```

Process-critical startup uses the Must pattern: config load, embedder
construction, reasoner construction, and store open all panic on failure
so the service can't come up half-broken.

**Database schema** (one DB per user, scoped by `project_id`):

- `memories(id, uuid, project_id, content, tags_json, created_at, updated_at)`
  - `id` is local autoincrement; `uuid` is the cross-machine identifier.
- `memory_vectors` - sqlite-vec `vec0` virtual table, dimension-bound at
  creation. Switching to an embedder with a different dim or ID is refused
  at startup; delete the DB to change embedders.
- `usage` - per-call event log: provider, model, tokens, cost, latency.
- `meta` - key/value: `embed_id`, `embed_dim`, per-project watermarks.

**Reconcile** is a two-pass algorithm:

1. Stream every event file. Track `knownCreates` (every UUID that has ever
   had a `create` event) and, for events newer than the watermark, a
   per-UUID target state.
2. Apply per-UUID state to the local DB. Reuse a logged embedding when
   `reuse_embeddings` is on and the model + dim match and the embedding
   event's filename is newer than the latest content-changing event for
   that UUID. Otherwise embed locally and (if `log_embeddings`) publish a
   new embedding event for our model.

After applying events, any local row whose UUID isn't in `knownCreates`
gets a `create` event (and, if logging is on, an `embedding` event with
the existing local vector) so teammates pick it up.

## Desktop server (Go + React)

The desktop "app" is a single Go binary (`yullu`) that serves a
React UI, a REST API, and the MCP endpoint on the same port - the browser
is the UI, there's no native window. Same SQLite DB, same `config.toml`,
and the same `*server.Server` instance powers the UI and MCP, so anything
you do in the browser is visible to the LLM and vice versa.

**Stack:**
- **Go `net/http`** with a single `ServeMux`: `/` (embedded SPA), `/api/*`
  (REST handlers in `cmd/app/api.go`), `/mcp` (`mark3labs/mcp-go`'s
  Streamable HTTP handler, hot-swappable when `SaveConfig` rebuilds the
  Server).
- **React 18** + **TypeScript** + **Vite** - frontend, built into
  `cmd/app/frontend/dist/` and embedded via `//go:embed`.
- **TanStack Router** - code-based routes (`/stats`, `/memories`, `/graph`,
  `/dreaming`, `/settings`).
- **TanStack Query** - thin fetch wrappers in `src/lib/api.ts`, hooks in
  `src/lib/queries.ts` (one per `App.*` method).
- **shadcn/ui** + **Tailwind CSS** - Radix-backed components in
  `src/components/ui/`. Theme tokens (CSS variables) in `src/index.css`,
  defaulted to dark mode.
- **Recharts** for stats charts; **react-force-graph-2d** for the
  similarity-and-tag graph.

```bash
# One-time:
brew install oven-sh/bun/bun                  # Bun runtime

# Build + run the desktop server:
make start
# Open http://localhost:47823

# HMR dev loop (vite on :5173 proxies /api and /mcp to :47823):
make dev
# Open http://localhost:5173
```

On first run `make start` will `bun install` in `cmd/app/frontend/`
(downloads node_modules - ~250MB), build the frontend, and compile the Go
binary with the dist embedded.

To add another shadcn component (e.g. `Tabs`, `Dialog`):
```bash
cd cmd/app/frontend
bunx shadcn@latest add tabs dialog
```

## Development

```bash
make dev      # vite + air, hot reload on both sides
make test     # unit + integration tests (incl. two-machine reconcile sim)
make smoke    # quick MCP wire-format round-trip
```

The Go test suite covers store CRUD, the memlog writer/reader, the full
reconcile pipeline using a fake embedder that simulates two developers
(same model → embedding reuse path; different model → local embed +
publish path), and the dream pipeline with a fake reasoner (parser,
round-trip create/update, reasoner-error paths that keep messages for
retry).

## Troubleshooting

| symptom | fix |
|---|---|
| `sqlite-vec not available` | The binary was built without cgo or against the wrong SQLite. Rebuild with `make build`. |
| `voyage embedder selected but no API key` | Set `VOYAGE_API_KEY` (or `[voyage].api_key`). Free tier at voyageai.com. |
| `embedding dimension mismatch` | You changed embedders. Delete `~/.local/share/yullu/memories.db` (events in `.yullu/` survive - reconcile rebuilds the DB). |
| `could not determine embedding dimension` | The embedder can't reach its provider. Check network / API key validity. |
| `no reasoner available` during dream | Configure `[reasoning].provider = "anthropic"` or `"openai"` with an API key, OR run dreaming only via `dream_now` from a client that supports sampling. |
| Teammate's memories not appearing | `git pull` first, then restart the server (or call `reconcile_memories`). |
| `sync enabled but no git repo` log line | The server's CWD isn't inside a git repo. Sync silently disables; local-only mode still works. |
| `listen tcp :47823: bind: address already in use` | Another `yullu` is already running. `lsof -ti :47823 \| xargs kill` then retry. |

## Name

*Yul'lu* is a Butchella (Badtjala) word for bottlenose dolphins. The Butchella
people are the traditional owners of K'gari (Fraser Island) in southeast
Queensland, Australia. Dolphins remember pod-mate signature whistles for
decades - a fitting metaphor for a tool that gives AI assistants persistent
memory across sessions. We acknowledge the Butchella people as the custodians
of K'gari and of the language this name comes from, and we pay our respects
to Elders past and present.
