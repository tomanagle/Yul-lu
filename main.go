// Yul'lu is the desktop server. It serves the React
// frontend, a REST API, and the MCP endpoint all on the same port. The
// browser is the UI - there's no native window, just a long-running HTTP
// server. Register MCP clients against http://localhost:47823/mcp.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tomanagle/yullu/internal/handlers"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed skills/body.md
var skillBody string

const httpAddr = ":47823"

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			os.Exit(runInstall(os.Args[2:]))
		case "uninstall":
			os.Exit(runUninstall(os.Args[2:]))
		case "stdio":
			os.Exit(runStdio())
		case "record-turn":
			os.Exit(runRecordTurn())
		case "inject":
			os.Exit(runInject())
		case "export":
			// Subcommand of subcommand: `yullu export agents-md [...]`.
			// Future exports (e.g. `yullu export json`) slot in here.
			if len(os.Args) < 3 {
				println("yullu export: missing format. Try `yullu export agents-md`.")
				os.Exit(2)
			}
			switch os.Args[2] {
			case "agents-md":
				os.Exit(runExportAgentsMD(os.Args[3:]))
			default:
				println("yullu export: unknown format:", os.Args[2])
				os.Exit(2)
			}
		case "version", "--version", "-v":
			println("Yul'lu " + version)
			return
		case "help", "--help", "-h":
			printUsage()
			return
		default:
			println("yullu: unknown subcommand:", os.Args[1])
			printUsage()
			os.Exit(2)
		}
	}

	app := NewApp()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	app.startup(ctx)
	defer app.shutdown(ctx)

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}

	mux := http.NewServeMux()
	handlers.Register(mux, handlers.RegisterParams{
		Status:               app,
		Config:               app,
		Memory:               app,
		Editor:               app,
		Projects:             app,
		Graph:                app,
		Stats:                app,
		Usage:                app,
		Dreamer:              app,
		Session:              app,
		DreamStats:           app,
		ProjectOverrides:     app,
		Messages:             app,
		SessionBuffer:        app,
		DreamPrompt:          app,
		DreamProgress:        app,
		DreamPasses:          app.store,
		Rater:                app,
		Recall:               app,
		Retrievals:           app,
		DreamContextMemories: app.DreamContextMemories,
	})
	mux.Handle("/mcp", mcpProxy{app: app})
	mux.Handle("/mcp/", mcpProxy{app: app})
	mux.Handle("/", spaHandler(dist))

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// Bind the listener before opening the browser so the kernel queues
	// the browser's first connection while Serve is still spinning up —
	// no curl-polling, no "connection refused" race.
	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	app.logger.Info("yul'lu starting",
		"url", "http://localhost"+httpAddr,
		"mcp", "http://localhost"+httpAddr+"/mcp",
	)

	// `make start` sets YULLU_OPEN_BROWSER=1 so the UI pops automatically
	// after the binary boots. Service units, scripts, and bare `yullu`
	// invocations leave it unset (no surprise window).
	if shouldOpenBrowser() {
		go openBrowser("http://localhost" + httpAddr)
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

// shouldOpenBrowser is true when YULLU_OPEN_BROWSER is set to a
// truthy value. Defaults to false so launchd/systemd units and any
// scripted use don't get a surprise browser window.
func shouldOpenBrowser() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("YULLU_OPEN_BROWSER")))
	switch v {
	case "1", "true", "yes", "y":
		return true
	}
	return false
}

// openBrowser launches the host OS's default browser at the given URL.
// Best-effort: a failure to launch (headless box, missing helper) is
// logged at debug level but doesn't fail the server boot.
//
// The launch tool (open/xdg-open/rundll32) exits immediately after
// dispatching the browser, so we reap it asynchronously to avoid a
// zombie process accumulating for the lifetime of the parent. The
// reaper goroutine costs ~2KB of stack and runs once per browser
// launch (≤ 1× per server boot).
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, freebsd, openbsd, etc.
		cmd = "xdg-open"
		args = []string{url}
	}
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		return
	}
	// Wait reaps the child so it doesn't become a zombie. Discard the
	// result — exit status of `open`/`xdg-open` doesn't tell us anything
	// useful about whether the browser actually opened.
	go func() { _ = c.Wait() }()
}

// spaHandler serves the embedded SPA. Static files (assets/*.js, /favicon.ico,
// etc.) are served as-is; anything else falls back to index.html so the React
// router can resolve client-side routes on hard refresh.
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := dist.Open(path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// mcpProxy delegates each /mcp request to whichever Server the App currently
// holds. SaveConfig swaps the underlying *Server when the embedder or
// reasoner changes, so capturing a single handler at boot would leave the
// transport pointing at a closed store after the user reconfigures.
type mcpProxy struct {
	app *App
}

func (p mcpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := p.app.mcpHandlerHTTP()
	if h == nil {
		http.Error(w, "yullu: finish setup in the UI before connecting an MCP client", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

const version = "0.1.0"

func printUsage() {
	println(`Yul'lu - persistent memory for AI coding assistants

Usage:
  yullu                     Start the desktop server (UI + REST + MCP on :47823)
  yullu install [target…]   Install skill + register MCP for an assistant.
                            Targets: claude, codex, cursor (default: claude and
                            codex if their config dirs exist). Flags:
                              --service       install launchd/systemd auto-start
                                              without prompting
                              --no-service    skip the auto-start install entirely
                              --yes / -y      answer yes to any interactive prompts
  yullu uninstall [target…] Reverse install (also removes the service unit)
  yullu stdio               Run MCP over stdio (legacy - prefer HTTP via the server)
  yullu record-turn         Internal: reads Claude Code Stop hook payload on stdin
                            and records the last turn. Wired into ~/.claude/settings.json
                            by 'yullu install claude'; not meant to be called directly.
  yullu inject              Internal: reads Claude Code UserPromptSubmit hook
                            payload on stdin and prints relevant memories to stdout
                            as context. Wired by 'yullu install claude'.
  yullu export agents-md    Write a categorised AGENTS.md from the memory store.
                            Flags:
                              --out PATH      output path (default ./AGENTS.md; '-' for stdout)
                              --project ID    project to export (default: cwd's git remote)
  yullu version             Print version
  yullu help                Show this help

Once running, register manually with:
  claude mcp add yullu --transport http http://localhost:47823/mcp`)
}
