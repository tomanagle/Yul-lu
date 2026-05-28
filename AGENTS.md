# Working in this repo

Context for AI coding assistants (Claude Code, Codex, Cursor, etc.)
working in the Yul'lu codebase. Read this top-to-bottom before touching
code — most of it answers questions you'd otherwise ask.

If you change something covered here, update this file in the same
commit so the next agent (or human) has accurate context.

---

## What this is

Yul'lu is a persistent-memory tool for AI coding assistants. A single Go
binary serves three surfaces on `localhost:47823`:

- `/` — React UI (embedded via `//go:embed all:frontend/dist`)
- `/api/*` — JSON REST
- `/mcp` — Streamable HTTP MCP endpoint (mark3labs/mcp-go)

Memories are stored in SQLite (`~/.local/share/yullu/memories.db`) with
sqlite-vec for embedding search and FTS5 for BM25 text search. Sync
across teammates happens via `.yullu/events/*.json` committed to the
repo.

`Yul'lu` is the **display** spelling (with apostrophe). `yullu` is the
**identifier** — used everywhere an apostrophe can't survive: binary
name, Go module path (`github.com/tomanagle/yullu`), env vars (`YULLU_*`),
file paths (`~/.local/share/yullu/`, `.yullu/`), domain (`yullu.ai`),
MCP server name. **Don't say `memorable-mcp`** — that was the pre-rename
name and any reference to it is stale.

---

## Repo layout

Flat at the root — there is no `cmd/` directory. If you see imports
pointing at `github.com/tomanagle/yullu/cmd/internal/...`, those are
stale; the correct path is `github.com/tomanagle/yullu/internal/...`.

```
yullu/
├── main.go             entry + subcommand dispatch
├── app.go              App struct; implements every handler interface
├── install.go          `yullu install` + per-assistant wiring
├── stdio.go            `yullu stdio` subcommand
├── record_turn.go      `yullu record-turn` (Claude Code Stop hook entry)
├── service.go          launchd/systemd auto-start install
├── config_writer.go    TOML round-trip for SaveConfig
├── skills/body.md      The SKILL.md body that gets installed to assistants
├── frontend/           React 18 + TanStack Router/Query + shadcn/ui
│   └── src/lib/{api,schemas,queries,project-scope}.ts
└── internal/
    ├── ai/             Embedder + Reasoner interfaces; providers
    ├── applog/         slog setup
    ├── config/         Config + ConfigOverride (per-project) + resolver
    ├── handlers/       One REST handler per file, GOTTH-style DI
    ├── memlog/         .yullu/events/ writer + reader
    ├── scope/          project_id resolution (git remote → path → cwd)
    ├── server/         MCP tool handlers, Dream + Reconcile algorithms
    └── store/          SQLite + sqlite-vec, schema migrations
```

The frontend embeds at compile time. If you're about to `go build` or
`go test` and `frontend/dist/` doesn't exist, run `make ensure-dist`
first (or just `make build`, which handles it).

---

## Build & run

```bash
make install        # builds frontend + Go binary, installs to $GOPATH/bin/yullu
make dev            # vite :47824 + air :47823 (HMR)
make test           # go test ./...   (CI runs this)
make vet            # go vet ./...    (CI runs this)
make smoke          # MCP stdio wire-format round-trip
```

**Dev port**: when running `make dev`, **open `http://localhost:47824`**.
`:47823` serves the *embedded production build* baked into the binary
and won't hot-reload. Vite proxies `/api` and `/mcp` from 47824 → 47823
transparently, so all data calls work. (We deliberately chose 47824
instead of Vite's default 5173 to avoid colliding with other local
Vite apps.)

**Building requires cgo + a C toolchain** (for `mattn/go-sqlite3` and
sqlite-vec). On macOS that's Xcode CLT; on Linux it's `build-essential`.

**Build tag `sqlite_fts5`** is required (set automatically by the
Makefile). Don't run raw `go test ./...` without `-tags sqlite_fts5` —
it'll fail with "no such module: fts5".

---

## Distribution

The only documented install path is `git clone` + `make install`. No
Homebrew tap, no npm package, no pre-built binaries yet. Don't add
references to those install paths in docs.

After building, `yullu install` wires the binary into an AI assistant:

- **Claude Code** — writes `~/.claude/skills/yullu/SKILL.md`, runs
  `claude mcp add yullu --transport http http://localhost:47823/mcp`,
  adds a **Stop hook** to `~/.claude/settings.json` that calls
  `yullu record-turn` so `record_messages` fires deterministically
  after every turn.
- **Codex CLI** — writes a yullu section to `~/.codex/AGENTS.md`, adds
  `[mcp_servers.yullu]` to `~/.codex/config.toml`. No hook support; the
  model has to honour the rules file.
- **Cursor** — adds `mcpServers.yullu` to `~/.cursor/mcp.json`. Same
  no-hooks caveat; prints a rules snippet for the user to commit at
  `.cursor/rules/yullu.mdc` if they want it.

`yullu install --service` (or interactive prompt) drops a launchd plist
(macOS) or systemd user unit (Linux) so the server auto-starts on
login. `yullu uninstall` reverses everything.

---

## Backend conventions

### Handler DI

Every REST handler is a struct + `New<Name>Handler(params)` constructor
+ `ServeHTTP` method, one file per route in `internal/handlers/`. The
constructor takes a `<Name>HandlerParams` struct with the **smallest
interface** that satisfies the handler. Interfaces live in
`internal/handlers/deps.go` (consumer-defined). `*App` happens to
satisfy all of them; `routes.go`'s `Register(mux, RegisterParams{})`
wires the binding.

When adding a new endpoint, follow the pattern: create
`getfoo.go` / `postfoo.go`, define a new interface in `deps.go` if
needed, register the route in `routes.go`, add an `App` method to
satisfy it.

### List response envelope

Every collection response is wrapped: `{"items": [...]}`. Never return
a bare JSON array. Server side uses `writeList[T]` in
`internal/handlers/json.go` (coerces nil slices to `[]` so clients
never see `"items": null`). Frontend uses `requestList(schema, path)`
in `lib/api.ts` which unwraps before returning — components still get
plain arrays. Rationale: forward-compat for pagination metadata + a
historical CSRF/JSON-hijacking concern.

### Config resolution

Three layers, applied in order (last wins):

1. Global `config.toml` in repo root
2. Repo-committed `.yullu/config.toml` (team-shared knobs)
3. User-private `~/.config/yullu/projects/<sanitized_project_id>.toml`
   (API keys, personal overrides)

Layer 2 **refuses API keys on load** (warning surfaced via the response
to `GET /api/projects/{id}/overrides`); committed files must not carry
secrets. Layer 1 (`config.toml`) is gitignored so users can't commit
their own keys by mistake.

**Embedding provider/model is deliberately not overridable per
project.** The SQLite store locks `embed_id` + `embed_dim` at first
write — changing per project would corrupt the vector index. The
`ConfigOverride` schema in `internal/config/override.go` omits an
embedding section to make this impossible to express.

### MCP tool handlers

In `internal/server/server.go`, `registerTools()` is the single source
of truth for which MCP tools yullu exposes. Each tool routes to a
`handle<Name>` method below. New tools: register in `registerTools()`,
implement the handler, add a doc paragraph to `skills/body.md`.

### `record_messages` is the hot path

Skills tell the assistant to call `record_messages` after every
substantive turn. Without those messages, the dreamer has nothing to
extract memories from. Don't soften the rule — the skill body's
language is intentionally directive ("REQUIRED after every substantive
turn"). For Claude Code specifically, the Stop hook (`yullu record-turn`)
makes this deterministic.

---

## Frontend conventions

### Types come from zod, not a types file

There is **no `frontend/src/lib/types.ts`** — it was deleted. All API
response shapes are defined as zod schemas in `lib/schemas.ts`, with
the TS type inferred via `z.infer<typeof XSchema>` and exported
alongside. `api.ts` parses every response through the matching schema,
so wire-format drift between Go and React throws a `ZodError` at the
boundary instead of corrupting downstream rendering.

When adding a new API response shape: add the schema to `schemas.ts`,
export the inferred type, use it in `api.ts` via `request(schema, ...)`
or `requestList(listSchema, ...)`.

**Component prop types stay inline** above each component:

```tsx
type StatTileProps = { label: string; value: number };
function StatTile({ label, value }: StatTileProps) { … }
```

Never put component prop types in a shared barrel.

### Palette

Night indigo + dream violet on deep navy. The aesthetic comes from
the ui-ux-pro-max skill's "Sleep Tracker" palette, chosen because
yullu means "bottlenose dolphins" in Butchella and the memory/dream
metaphor is what the design leans into.

Tokens live in `frontend/src/index.css` as HSL CSS variables. shadcn
components read them automatically. For chart/canvas code that can't
read CSS vars (Recharts, react-force-graph), the constants are inlined
in `frontend/src/components/charts.tsx` and
`frontend/src/components/memory-graph.tsx` — keep those in lockstep
with `index.css`.

Inter is the only font, loaded via Google Fonts CDN. Typography
discipline: display 700 + tight tracking, headings 600, body 400,
labels 500 + uppercase + +0.08em letter-spacing.

### Project scope

Every data page (Stats, Memories, Graph, Dreaming) reads the current
project from `useProjectScope()` (context provider in
`lib/project-scope.tsx`). The sidebar's project picker is the **only**
project picker — pages don't have their own. When adding a new
project-scoped page, just call `useProjectScope()`; don't add a local
state for it.

### Stack details to know

- TanStack Router (code-based, not file-based) — see `router.tsx`
- TanStack Query for server state — hooks in `lib/queries.ts`
- shadcn/ui via Tailwind — components in `components/ui/`
- Recharts for stats, react-force-graph-2d for the memory graph
- Bun is the package manager (not npm/yarn/pnpm). Use `bun install`,
  `bun run X`, never `npm`.

---

## Working with Yul'lu's own MCP tools

When you (the agent) are working in this repo, your AI client should
have yullu's MCP tools available (`mcp__yullu__*`). The skill body
(`skills/body.md`) tells you when to call them; the short version:

- **Before answering codebase questions** — `retrieve_memories`
- **When the user makes a decision worth keeping** — `store_memory`
- **After every substantive turn** — `record_messages`

If those tools aren't available in your session, yullu probably isn't
running. Start it with `yullu` (or `make start`).

The memories that already exist in the store cover most of this file's
content plus more. If you're unsure about a convention, search before
guessing.

---

## CI + pre-commit

`.github/workflows/ci.yml` runs on every PR to `main`:

- Frontend: `bun run lint` (oxlint) + `bun run fmt:check` (oxfmt)
- Go: `make vet` then `make test`

Lefthook hooks auto-fix at commit time (oxlint --fix, oxfmt, `make fmt`).
Contributors run `lefthook install` once after cloning. If you push a
commit with formatting issues, CI will fail the PR — fix locally and
push again.

`make fmt` runs `gofmt -s -w .` across the whole tree. The lefthook hook
fires only when Go files are staged.

---

## Things to avoid

- **Don't commit `config.toml`.** It's gitignored. Yullu regenerates a
  blank default on first run if missing.
- **Don't reintroduce `frontend/src/lib/types.ts`.** Use zod schemas.
- **Don't change the embedding provider/model per project** in any
  override layer. The store will reject it.
- **Don't bypass `record_messages`** when working as the AI agent
  in this repo — the whole product depends on the buffer being fed.
- **Don't reference `memorable-mcp`** in code, docs, or commit
  messages. It was renamed to yullu/Yul'lu in May 2026.
- **Don't add references to `master`** as the default branch — the
  default branch is `main`.
- **Don't use plain `git push --force`** to overwrite shared history.
  Use `--force-with-lease` so a concurrent update fails the push
  instead of silently clobbering it.
- **Don't run `npm`** anywhere in `frontend/`. Use `bun`.

---

## Quick reference: where things live

| Thing | Path |
|---|---|
| New REST endpoint | `internal/handlers/getfoo.go` + register in `routes.go` |
| New MCP tool | `internal/server/server.go` `registerTools()` + handler |
| New API type | zod schema in `frontend/src/lib/schemas.ts` |
| Component prop types | inline above the component |
| SQLite schema change | `internal/store/store.go` `init()` schema slice + migrator |
| Per-project config knob | `internal/config/override.go` (pointer-typed) |
| Color/font tokens | `frontend/src/index.css` |
| MCP skill content | `skills/body.md` (used by `yullu install`) |
| CI checks | `.github/workflows/ci.yml` |
| Pre-commit hooks | `lefthook.yml` |
| Branding rules | this file, "What this is" section |
