BINARY      := yullu
BIN_DIR     := bin
BIN         := $(BIN_DIR)/$(BINARY)
PKG         := github.com/tomanagle/yullu
GOPATH_BIN   = $(shell go env GOPATH)/bin
INSTALLED    = $(GOPATH_BIN)/$(BINARY)

# Build tags. sqlite_fts5 turns on the FTS5 module in mattn/go-sqlite3 so the
# memory search box can use SQLite's built-in BM25 ranking (free + local).
GO_TAGS     := sqlite_fts5

# Silence macOS sqlite3_auto_extension deprecation warnings from cgo's prolog.
export CGO_CFLAGS := $(CGO_CFLAGS) -Wno-deprecated-declarations

FRONTEND_DIR  := frontend
FRONTEND_DIST := $(FRONTEND_DIR)/dist

.DEFAULT_GOAL := help

.PHONY: help start dev build install refresh run register smoke \
        test tidy fmt vet clean frontend-deps frontend-build ensure-dist \
        air-install ensure-bun

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "First-time install:  make install  (then run \`yullu install\` to wire your AI assistant)"
	@echo "Just run it:         make start    (builds + launches at http://localhost:47823)"
	@echo "Requires:   Bun (brew install oven-sh/bun/bun); a Voyage API key (free at voyageai.com)"

# ---------------------------------------------------------------------------
# Primary entry - build everything, run the desktop server, open the browser.
# The same process serves the UI, the REST API, and the MCP endpoint, so
# `claude mcp add yullu --transport http http://localhost:47823/mcp` wires
# the LLM up.
# ---------------------------------------------------------------------------

# YULLU_OPEN_BROWSER=1 tells the binary to launch the host's default browser
# at the dashboard URL once the listener is bound. Bare `yullu` (or a service
# unit) leaves it unset, so no surprise window outside of `make start`.
start: install ## Build + launch the desktop server (http://localhost:47823) and open it in the browser
	@echo
	@echo "yullu starting at http://localhost:47823"
	@echo "MCP endpoint:           http://localhost:47823/mcp"
	@echo "Register with Claude:   claude mcp add yullu --transport http http://localhost:47823/mcp"
	@echo
	YULLU_OPEN_BROWSER=1 $(INSTALLED)

# ---------------------------------------------------------------------------
# Dev loop - vite serves the frontend on :5173 with HMR; Go server runs on
# :47823. Vite proxies /api and /mcp to Go so a single tab works in dev.
# Open http://localhost:5173 - NOT 47823 - during dev.
# ---------------------------------------------------------------------------

dev: air-install frontend-deps ensure-dist ## Hot-reload dev: vite (:47824) + air-rebuilt Go server (:47823), opens browser
	@echo ""
	@echo "  ┌─────────────────────────────────────────────────────────┐"
	@echo "  │  Opening  http://localhost:47824  in your browser…      │"
	@echo "  │  (vite dev server with hot module reload)               │"
	@echo "  │                                                         │"
	@echo "  │  NOT :47823 — that's the embedded prod build, no HMR.   │"
	@echo "  │  :47824 proxies /api and /mcp to :47823 automatically.  │"
	@echo "  └─────────────────────────────────────────────────────────┘"
	@echo ""
	@bash -c 'trap "kill 0" EXIT; \
		(cd $(FRONTEND_DIR) && bun run dev) & \
		if command -v jq >/dev/null 2>&1; then \
			air -c .air.toml 2>&1 | jq -R -r --unbuffered "fromjson? // ." & \
		else \
			echo "[make dev] jq not found — server logs shown as raw JSON (brew install jq to pretty-print them)"; \
			air -c .air.toml & \
		fi; \
		wait'

# ensure-dist guarantees $(FRONTEND_DIST) exists so `go build` doesn't choke
# on the //go:embed directive in dev. We never *see* the embedded frontend
# during `make dev` - Vite serves the real one on :5173 - but the embed
# directive is checked at compile time. If a real prod bundle is already
# there we leave it alone.
ensure-dist:
	@mkdir -p $(FRONTEND_DIST)
	@[ -f $(FRONTEND_DIST)/index.html ] || \
		printf '<!doctype html><title>yullu dev placeholder</title>' > $(FRONTEND_DIST)/index.html

# ---------------------------------------------------------------------------
# Build targets
# ---------------------------------------------------------------------------

build: frontend-build ## Compile to ./bin/yullu
	@mkdir -p $(BIN_DIR)
	go build -tags $(GO_TAGS) -o $(BIN) .
	@echo "built $(BIN)"

install: frontend-build ## Build + install to $$GOPATH/bin/yullu
	go build -tags $(GO_TAGS) -o $(INSTALLED) .
	@echo "installed $(INSTALLED)"

# refresh rebuilds the Go binary AND re-points the Stop hook at it. Use
# this whenever the hook is firing a stale build and you can't tell why —
# e.g. after editing record_turn.go, install.go, or anything in the
# resolveProjectID chain. Or when `yullu install` was last run from
# ./bin/yullu instead of $GOPATH/bin/yullu and the hook now points at the
# wrong path.
#
# Skips the frontend rebuild — the hook doesn't care about the UI bundle.
# Depends on ensure-dist so //go:embed all:frontend/dist still resolves.
# Run `make build` separately if you also want the latest UI baked in.
#
# Non-interactive: --yes --no-service skip the launchd/systemd prompt.
# Run `yullu install --service` by hand if you want the auto-start unit.
refresh: ensure-dist ## Rebuild the Go binary + re-point the Stop hook at it (no frontend rebuild)
	go build -tags $(GO_TAGS) -o $(INSTALLED) .
	@echo "installed $(INSTALLED)"
	@echo
	@echo "Re-installing Stop hook to use $(INSTALLED)…"
	$(INSTALLED) install --yes --no-service
	@echo
	@echo "Done. If make dev / make start is running, restart it so the HTTP server picks up the new build too."

run: install ## Build and run the server
	$(INSTALLED)

frontend-build: frontend-deps
	cd $(FRONTEND_DIR) && bun run build

frontend-deps: ensure-bun
	@if [ ! -d "$(FRONTEND_DIR)/node_modules" ]; then \
		echo "installing frontend deps..."; \
		cd $(FRONTEND_DIR) && bun install; \
	fi

ensure-bun:
	@command -v bun >/dev/null 2>&1 || (\
		echo "Bun is required. Install: brew install oven-sh/bun/bun  (or see https://bun.sh)" >&2; \
		exit 1)

air-install:
	@command -v air >/dev/null 2>&1 || (echo "installing air..."; go install github.com/air-verse/air@latest)

# ---------------------------------------------------------------------------
# Misc
# ---------------------------------------------------------------------------

register: ## Print the claude mcp add command for the desktop server
	@echo
	@echo "Make sure the server is running ('yullu' or 'make start'), then:"
	@echo "  claude mcp add yullu --transport http http://localhost:47823/mcp"
	@echo

smoke: build ## Round-trip initialize + tools/list over stdio (no API key needed)
	@{ \
		printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'; \
		printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'; \
		printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; \
	} | ./$(BIN) stdio 2>/dev/null | head -c 4000
	@echo

# ---------------------------------------------------------------------------
# Quality
# ---------------------------------------------------------------------------

test: ensure-dist ## Run unit tests (depends on dist existing so //go:embed resolves)
	go test -tags $(GO_TAGS) ./...

tidy: ## go mod tidy
	go mod tidy

fmt: ## Format Go sources
	gofmt -s -w .

vet: ensure-dist ## Run go vet (depends on dist existing so //go:embed resolves)
	go vet -tags $(GO_TAGS) ./...

clean: ## Remove built artifacts
	rm -rf $(BIN_DIR) $(FRONTEND_DIST)
