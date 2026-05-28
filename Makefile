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

.PHONY: help start dev build install run register smoke \
        test tidy fmt vet clean frontend-deps frontend-build ensure-dist \
        air-install ensure-bun

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "Primary:    make start    (build + launch the desktop server at http://localhost:47823)"
	@echo "Requires:   Bun (brew install oven-sh/bun/bun); a Voyage API key (free at voyageai.com)"

# ---------------------------------------------------------------------------
# Primary entry - build everything, run the desktop server, open the browser.
# The same process serves the UI, the REST API, and the MCP endpoint, so
# `claude mcp add yullu --transport http http://localhost:47823/mcp` wires
# the LLM up.
# ---------------------------------------------------------------------------

start: install ## Build + launch the desktop server (http://localhost:47823)
	@echo
	@echo "yullu starting at http://localhost:47823"
	@echo "MCP endpoint:           http://localhost:47823/mcp"
	@echo "Register with Claude:   claude mcp add yullu --transport http http://localhost:47823/mcp"
	@echo
	$(INSTALLED)

# ---------------------------------------------------------------------------
# Dev loop - vite serves the frontend on :5173 with HMR; Go server runs on
# :47823. Vite proxies /api and /mcp to Go so a single tab works in dev.
# Open http://localhost:5173 - NOT 47823 - during dev.
# ---------------------------------------------------------------------------

dev: air-install frontend-deps ensure-dist ## Hot-reload dev: vite (:47824) + air-rebuilt Go server (:47823)
	@echo ""
	@echo "  ┌─────────────────────────────────────────────────────────┐"
	@echo "  │  Open  http://localhost:47824  in your browser          │"
	@echo "  │  (vite dev server with hot module reload)               │"
	@echo "  │                                                         │"
	@echo "  │  NOT :47823 — that's the embedded prod build, no HMR.   │"
	@echo "  │  :47824 proxies /api and /mcp to :47823 automatically.  │"
	@echo "  └─────────────────────────────────────────────────────────┘"
	@echo ""
	@bash -c 'trap "kill 0" EXIT; \
		(cd $(FRONTEND_DIR) && bun run dev) & \
		air -c .air.toml & \
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
	@echo "Make sure the server is running ('make start'), then:"
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

test: ## Run unit tests
	go test -tags $(GO_TAGS) ./...

tidy: ## go mod tidy
	go mod tidy

fmt: ## Format Go sources
	gofmt -s -w .

vet: ## Run go vet
	go vet -tags $(GO_TAGS) ./...

clean: ## Remove built artifacts
	rm -rf $(BIN_DIR) $(FRONTEND_DIST)
